package sites

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/google/uuid"
)

// AddArchive extracts a "tar.gz" or "zip" archive into an open deployment,
// like AddFiles for every regular file in it. Entries are streamed and
// validated: absolute paths, "..", and other invalid paths (CleanPath),
// symlinks, hard links, devices, and other special entries reject the
// whole archive, as do more than MaxArchiveEntries entries, a file over
// MaxFileSize, or more than MaxArchiveSize uncompressed bytes. Directory
// entries are skipped. When every file sits under one common top-level
// directory (as `tar czf site.tar.gz site/` produces), that directory is
// stripped, so site/index.html publishes as index.html. Nothing is added
// unless the whole archive is valid.
func (s *Service) AddArchive(ctx context.Context, deployment uuid.UUID, r io.Reader, format string) error {
	d, err := s.openDeployment(ctx, deployment)
	if err != nil {
		return err
	}
	x := &extractor{ctx: ctx, svc: s, siteID: d.SiteID}
	switch format {
	case "tar.gz":
		err = x.tarGz(r)
	case "zip":
		err = x.zip(r)
	default:
		return inputErrorf("unsupported archive format %q: want tar.gz or zip", format)
	}
	if err != nil {
		return err
	}
	if len(x.files) == 0 {
		return inputErrorf("archive contains no files")
	}
	files, err := stripCommonDir(x.files)
	if err != nil {
		return err
	}
	return s.addStaged(ctx, deployment, files)
}

type extractor struct {
	ctx     context.Context
	svc     *Service
	siteID  string
	entries int
	total   int64
	files   []staged
}

// entry counts one archive entry against MaxArchiveEntries.
func (x *extractor) entry() error {
	x.entries++
	if x.entries > MaxArchiveEntries {
		return inputErrorf("archive has more than %d entries", MaxArchiveEntries)
	}
	return x.ctx.Err()
}

// dir validates a directory entry's name; directories are not published.
func (x *extractor) dir(name string) error {
	name = strings.TrimSuffix(name, "/")
	for strings.HasPrefix(name, "./") {
		name = name[2:]
	}
	if name == "" || name == "." {
		return nil
	}
	_, err := CleanPath(name)
	return archiveErr(err)
}

// file stores one regular file's blob and stages it.
func (x *extractor) file(name string, declared int64, r io.Reader) error {
	p, err := CleanPath(name)
	if err != nil {
		return archiveErr(err)
	}
	if declared > MaxFileSize {
		return inputErrorf("archive file %q is larger than %d bytes", p, MaxFileSize)
	}
	remaining := MaxArchiveSize - x.total
	sp, err := spool(r, min(MaxFileSize, remaining))
	if err != nil {
		if errors.Is(err, errTooLarge) {
			if remaining < MaxFileSize {
				return inputErrorf("archive is larger than %d bytes uncompressed", MaxArchiveSize)
			}
			return inputErrorf("archive file %q is larger than %d bytes", p, MaxFileSize)
		}
		return fmt.Errorf("sites: read archive file %q: %w", p, err)
	}
	defer sp.close()
	x.total += sp.size
	if err := x.svc.putBlob(x.ctx, x.siteID, sp.sum, sp.size, sp.reader); err != nil {
		return err
	}
	x.files = append(x.files, staged{path: p, contentType: ContentTypeFor(p), sum: sp.sum, size: sp.size})
	return nil
}

func archiveErr(err error) error {
	var ie *InputError
	if errors.As(err, &ie) {
		return &InputError{Msg: "archive entry: " + ie.Msg}
	}
	return err
}

func (x *extractor) tarGz(r io.Reader) error {
	// Bound the compressed input too, so a stream of empty entries cannot
	// run forever.
	zr, err := gzip.NewReader(&limitedReader{r: r, n: MaxArchiveSize})
	if err != nil {
		return inputErrorf("invalid tar.gz archive: %v", err)
	}
	defer zr.Close()
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if errors.Is(err, tar.ErrInsecurePath) {
			return inputErrorf("archive contains an insecure path")
		}
		if errors.Is(err, errTooLarge) {
			return inputErrorf("archive is larger than %d bytes", MaxArchiveSize)
		}
		if err != nil {
			return inputErrorf("invalid tar.gz archive: %v", err)
		}
		if err := x.entry(); err != nil {
			return err
		}
		switch h.Typeflag {
		case tar.TypeReg:
			err = x.file(h.Name, h.Size, tr)
		case tar.TypeDir:
			err = x.dir(h.Name)
		case tar.TypeXGlobalHeader:
			// PAX global metadata, not a file.
		case tar.TypeSymlink, tar.TypeLink:
			err = inputErrorf("archive entry %q: links are not allowed", h.Name)
		case tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
			err = inputErrorf("archive entry %q: devices and pipes are not allowed", h.Name)
		default:
			err = inputErrorf("archive entry %q: unsupported entry type %q", h.Name, h.Typeflag)
		}
		if errors.Is(err, errTooLarge) {
			return inputErrorf("archive is larger than %d bytes", MaxArchiveSize)
		}
		if err != nil {
			return err
		}
	}
}

func (x *extractor) zip(r io.Reader) error {
	// zip needs random access to the central directory, so spool the
	// archive (bounded) first.
	sp, err := spool(r, MaxArchiveSize)
	if errors.Is(err, errTooLarge) {
		return inputErrorf("archive is larger than %d bytes", MaxArchiveSize)
	}
	if err != nil {
		return fmt.Errorf("sites: read archive: %w", err)
	}
	defer sp.close()
	zr, err := zip.NewReader(sp.readerAt(), sp.size)
	if errors.Is(err, zip.ErrInsecurePath) {
		return inputErrorf("archive contains an insecure path")
	}
	if err != nil {
		return inputErrorf("invalid zip archive: %v", err)
	}
	for _, f := range zr.File {
		if err := x.entry(); err != nil {
			return err
		}
		mode := f.Mode()
		switch {
		case mode&fs.ModeSymlink != 0:
			return inputErrorf("archive entry %q: links are not allowed", f.Name)
		case mode.IsDir():
			if err := x.dir(f.Name); err != nil {
				return err
			}
		case !mode.IsRegular():
			return inputErrorf("archive entry %q: devices and other special files are not allowed", f.Name)
		default:
			if err := x.zipFile(f); err != nil {
				return err
			}
		}
	}
	return nil
}

func (x *extractor) zipFile(f *zip.File) error {
	declared := int64(MaxFileSize + 1)
	if f.UncompressedSize64 <= uint64(MaxFileSize) {
		declared = int64(f.UncompressedSize64)
	}
	rc, err := f.Open()
	if err != nil {
		return inputErrorf("archive entry %q: %v", f.Name, err)
	}
	defer rc.Close()
	err = x.file(f.Name, declared, rc)
	var ie *InputError
	if err != nil && !errors.As(err, &ie) && (errors.Is(err, zip.ErrChecksum) || errors.Is(err, zip.ErrFormat) || errors.Is(err, zip.ErrAlgorithm)) {
		return inputErrorf("archive entry %q: %v", f.Name, err)
	}
	return err
}

// stripCommonDir removes a single top-level directory shared by every
// file, and keeps the last occurrence of a repeated path.
func stripCommonDir(files []staged) ([]staged, error) {
	top, _, ok := strings.Cut(files[0].path, "/")
	if ok {
		for _, f := range files[1:] {
			if t, _, nested := strings.Cut(f.path, "/"); !nested || t != top {
				ok = false
				break
			}
		}
	}
	if !ok {
		return files, nil
	}
	out := make([]staged, len(files))
	for i, f := range files {
		p, err := CleanPath(f.path[len(top)+1:])
		if err != nil {
			return nil, archiveErr(err)
		}
		f.path = p
		out[i] = f
	}
	return out, nil
}

var errTooLarge = errors.New("sites: too large")

// limitedReader reads at most n bytes and then fails with errTooLarge
// (io.LimitReader would report a silent EOF).
type limitedReader struct {
	r io.Reader
	n int64
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.n <= 0 {
		// Probe for more input: only data beyond the limit is an error.
		var one [1]byte
		if n, _ := l.r.Read(one[:]); n > 0 {
			return 0, errTooLarge
		}
		return 0, io.EOF
	}
	if int64(len(p)) > l.n {
		p = p[:l.n]
	}
	n, err := l.r.Read(p)
	l.n -= int64(n)
	return n, err
}

// spooled is a fully read, hashed stream, in memory or in a temp file.
type spooled struct {
	mem  []byte
	file *os.File
	size int64
	sum  [sha256.Size]byte
}

// spool reads r to EOF, hashing it; more than limit bytes is errTooLarge.
// Up to memSpool bytes stay in memory, larger streams go to a temp file.
func spool(r io.Reader, limit int64) (*spooled, error) {
	h := sha256.New()
	lr := &limitedReader{r: r, n: limit}
	var buf bytes.Buffer
	n, err := io.CopyN(io.MultiWriter(&buf, h), lr, memSpool+1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	sp := &spooled{size: n}
	if n <= memSpool {
		sp.mem = buf.Bytes()
		sumInto(sp, h)
		return sp, nil
	}
	f, err := os.CreateTemp("", "handbill-spool-*")
	if err != nil {
		return nil, err
	}
	sp.file = f
	if _, err := f.Write(buf.Bytes()); err != nil {
		sp.close()
		return nil, err
	}
	rest, err := io.Copy(io.MultiWriter(f, h), lr)
	if err != nil {
		sp.close()
		return nil, err
	}
	sp.size += rest
	sumInto(sp, h)
	return sp, nil
}

func sumInto(sp *spooled, h hash.Hash) { copy(sp.sum[:], h.Sum(nil)) }

// reader returns the content from the start; valid until close.
func (sp *spooled) reader() (io.Reader, error) {
	if sp.file == nil {
		return bytes.NewReader(sp.mem), nil
	}
	if _, err := sp.file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return sp.file, nil
}

func (sp *spooled) readerAt() io.ReaderAt {
	if sp.file == nil {
		return bytes.NewReader(sp.mem)
	}
	return sp.file
}

func (sp *spooled) close() {
	if sp.file != nil {
		_ = sp.file.Close()
		_ = os.Remove(sp.file.Name())
	}
}
