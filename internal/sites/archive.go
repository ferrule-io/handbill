package sites

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"math"
	"os"
	"strings"

	"github.com/google/uuid"
)

// AddArchive extracts a "tar.gz" or "zip" archive into an open deployment
// created by by, like AddFiles for every regular file in it. Entries are
// streamed and validated: absolute paths, "..", and other invalid paths
// (CleanPath), symlinks, hard links, devices, and other special entries
// reject the whole archive, as do more than MaxArchiveEntries entries
// (declared or present in a zip's central directory), a file over
// MaxFileSize, or more than MaxArchiveSize uncompressed bytes (for tar.gz,
// more than maxTarStream decompressed bytes including tar metadata).
// Directory entries are skipped. When every file sits under one common
// top-level directory (as `tar czf site.tar.gz site/` produces), that
// directory is stripped, so site/index.html publishes as index.html.
// Nothing is added unless the whole archive is valid.
func (s *Service) AddArchive(ctx context.Context, deployment, by uuid.UUID, r io.Reader, format string) error {
	d, err := s.openDeployment(ctx, deployment, by)
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
		return tooManyEntries()
	}
	return x.ctx.Err()
}

func tooManyEntries() error {
	return inputErrorf("archive has more than %d entries", MaxArchiveEntries)
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
		if errors.Is(err, errArchiveTooLarge) {
			return err // the whole stream's limit, reported by the caller
		}
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
	// Bound the compressed input, so a stream of empty entries cannot run
	// forever, and the decompressed stream, so chained PAX or GNU
	// long-name headers, which are not entries and carry no file bytes,
	// cannot either.
	zr, err := gzip.NewReader(&limitedReader{r: r, n: MaxArchiveSize, err: errArchiveTooLarge})
	if err != nil {
		return inputErrorf("invalid tar.gz archive: %v", err)
	}
	defer zr.Close()
	tr := tar.NewReader(&limitedReader{r: zr, n: maxTarStream, err: errArchiveTooLarge})
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
	if err := checkZipDirectory(sp.readerAt(), sp.size); err != nil {
		return err
	}
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

// Zip record signatures and fixed lengths (APPNOTE.TXT 4.3).
const (
	zipCentralSig  = 0x02014b50
	zipCentralLen  = 46
	zipEndSig      = 0x06054b50
	zipEndLen      = 22
	zipEnd64LocSig = 0x07064b50
	zipEnd64LocLen = 20
	zipEnd64Sig    = 0x06064b50
	zipEnd64Len    = 56
	// zipEndSearch is how far from the end archive/zip looks for the end
	// record: a 64 KiB comment plus the record, rounded up.
	zipEndSearch = 65 * 1024
)

// checkZipDirectory bounds a zip's central directory before archive/zip
// reads it. zip.NewReader allocates a *zip.File for every central header
// it finds, reading headers until one fails to parse, and compares only
// the low 16 bits of the end record's entry count with what it read: a
// small archive could declare few entries and hold millions of headers. So
// the declared count (from the end record, or the zip64 end record) must
// not exceed MaxArchiveEntries, nor may the headers actually present at
// either offset archive/zip may start reading at. Locating the end record
// mirrors archive/zip's readDirectoryEnd. Nothing is allocated per entry.
func checkZipDirectory(r io.ReaderAt, size int64) error {
	records, dirSize, dirOffset, endOffset, err := readZipEnd(r, size)
	if err != nil {
		return inputErrorf("invalid zip archive: %v", err)
	}
	if records > MaxArchiveEntries {
		return tooManyEntries()
	}
	if records*zipCentralLen > dirSize {
		return inputErrorf("invalid zip archive: central directory too small for %d entries", records)
	}
	// archive/zip reads from the offset the end record's position implies
	// (data prepended to the archive shifts it) or, when a header parses
	// there, from the declared offset itself.
	for _, off := range []int64{endOffset - int64(dirSize), int64(dirOffset)} {
		if off >= 0 && off < size && zipHeaders(r, size, off) > MaxArchiveEntries {
			return tooManyEntries()
		}
	}
	return nil
}

// readZipEnd finds the end of central directory record in the last 1 KiB,
// then the last 65 KiB (its comment is up to 64 KiB), and follows a zip64
// locator when the record's fields overflow. It returns the declared entry
// count, the directory's size and offset, and the end record's offset.
func readZipEnd(r io.ReaderAt, size int64) (records, dirSize, dirOffset uint64, endOffset int64, err error) {
	le := binary.LittleEndian
	var end []byte
	for _, n := range []int64{1024, zipEndSearch} {
		n = min(n, size)
		buf := make([]byte, n)
		if _, err := r.ReadAt(buf, size-n); err != nil && err != io.EOF {
			return 0, 0, 0, 0, err
		}
		if p := zipEndSignature(buf); p >= 0 {
			end, endOffset = buf[p:], size-n+int64(p)
			break
		}
		if n == size {
			break
		}
	}
	if end == nil {
		return 0, 0, 0, 0, errors.New("no end of central directory record")
	}
	records, dirSize, dirOffset = uint64(le.Uint16(end[10:])), uint64(le.Uint32(end[12:])), uint64(le.Uint32(end[16:]))
	if records == 0xffff || dirSize == 0xffffffff || dirOffset == 0xffffffff {
		if loc := endOffset - zipEnd64LocLen; loc >= 0 {
			var l [zipEnd64LocLen]byte
			if _, err := r.ReadAt(l[:], loc); err != nil {
				return 0, 0, 0, 0, err
			}
			if le.Uint32(l[:]) == zipEnd64LocSig && le.Uint32(l[4:]) == 0 && le.Uint32(l[16:]) == 1 {
				p := int64(le.Uint64(l[8:]))
				var e [zipEnd64Len]byte
				if p < 0 {
					return 0, 0, 0, 0, errors.New("invalid zip64 locator")
				}
				if _, err := r.ReadAt(e[:], p); err != nil {
					return 0, 0, 0, 0, err
				}
				if le.Uint32(e[:]) != zipEnd64Sig {
					return 0, 0, 0, 0, errors.New("invalid zip64 end record")
				}
				records, dirSize, dirOffset, endOffset = le.Uint64(e[32:]), le.Uint64(e[40:]), le.Uint64(e[48:]), p
			}
		}
	}
	if dirSize > math.MaxInt64 || dirOffset > math.MaxInt64 {
		return 0, 0, 0, 0, errors.New("invalid central directory")
	}
	return records, dirSize, dirOffset, endOffset, nil
}

// zipEndSignature returns the offset in b of the last end record whose
// comment fits in b, or -1.
func zipEndSignature(b []byte) int {
	for i := len(b) - zipEndLen; i >= 0; i-- {
		if binary.LittleEndian.Uint32(b[i:]) == zipEndSig {
			if i+zipEndLen+int(binary.LittleEndian.Uint16(b[i+zipEndLen-2:])) > len(b) {
				return -1 // truncated comment, as archive/zip treats it
			}
			return i
		}
	}
	return -1
}

// zipHeaders counts the central directory headers starting at off, as
// archive/zip reads them, stopping past MaxArchiveEntries.
func zipHeaders(r io.ReaderAt, size, off int64) int {
	br := bufio.NewReader(io.NewSectionReader(r, off, size-off))
	le := binary.LittleEndian
	var h [zipCentralLen]byte
	n := 0
	for n <= MaxArchiveEntries {
		if _, err := io.ReadFull(br, h[:]); err != nil || le.Uint32(h[:]) != zipCentralSig {
			break
		}
		// Skip the name, extra field, and comment.
		if _, err := br.Discard(int(le.Uint16(h[28:])) + int(le.Uint16(h[30:])) + int(le.Uint16(h[32:]))); err != nil {
			break
		}
		n++
	}
	return n
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

var (
	errTooLarge = errors.New("sites: too large")
	// errArchiveTooLarge is a whole archive stream over its limit, as
	// opposed to one file in it.
	errArchiveTooLarge = fmt.Errorf("%w: archive", errTooLarge)
)

// limitedReader reads at most n bytes and then fails with err, or
// errTooLarge when err is nil (io.LimitReader would report a silent EOF).
type limitedReader struct {
	r   io.Reader
	n   int64
	err error
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.n <= 0 {
		// Probe for more input: only data beyond the limit is an error.
		var one [1]byte
		if n, _ := l.r.Read(one[:]); n > 0 {
			if l.err != nil {
				return 0, l.err
			}
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
