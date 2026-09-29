package compress

// The models below turn per-block codec output into what the kernel would allocate. Each
// rule is the kernel's, cited; each parameter is a named constant so a reader can check it.

// ZFSAshift is the pool sector shift the installer creates (zpool create -o ashift=12).
const ZFSAshift = 12

// ZFSModel describes an OpenZFS dataset write path.
type ZFSModel struct {
	Ashift int
	// EarlyAbort models OpenZFS >= 2.2 zstd early abort (module parameter
	// zfs_zstd_earlyabort_pass=1, the default): for zstd levels >= 3 on records of at least
	// 128 KiB, LZ4 is tried first; if LZ4 cannot shrink the record, zstd-1 is tried; if that
	// fails too the record is stored uncompressed without running the requested level.
	EarlyAbort bool
}

// earlyAbortMinLevel and earlyAbortMinSize are OpenZFS's zstd_cutoff_level (ZIO_ZSTD_LEVEL_3)
// and zstd_abort_size (128 KiB).
const (
	earlyAbortMinLevel = 3
	earlyAbortMinSize  = 128 << 10
)

func roundUp(n, to int) int { return (n + to - 1) / to * to }

// fits reports whether a compressed record is worth keeping: OpenZFS stores a record
// compressed only when that saves at least 1/8 of it (zio_compress_data's d_len =
// s_len - s_len>>3) and at least one sector after rounding.
func (m ZFSModel) fits(csize, lsize int) bool {
	if csize <= 0 || csize > lsize-lsize>>3 {
		return false
	}
	return roundUp(csize, 1<<m.Ashift) < lsize
}

// ZFSRecord is the outcome of writing one record.
type ZFSRecord struct {
	LSize, PSize int // logical and allocated (physical) bytes
	Compressed   bool
	Aborted      bool // early abort: the requested zstd level never ran
}

// Record decides one record. csize is the requested setting's compressed size, lz4 and
// zstd1 the sizes the early-abort passes would produce (ignored unless they apply); all
// three are already adjusted to ZFS's own framing (FrameOverhead).
func (m ZFSModel) Record(s Setting, lsize, csize, lz4, zstd1 int) ZFSRecord {
	sector := 1 << m.Ashift
	raw := ZFSRecord{LSize: lsize, PSize: roundUp(lsize, sector)}
	if s.Codec == "off" {
		return raw
	}
	if m.EarlyAbort && s.Codec == "zstd" && s.Level >= earlyAbortMinLevel && lsize >= earlyAbortMinSize {
		if !m.fits(lz4, lsize) && !m.fits(zstd1, lsize) {
			raw.Aborted = true
			return raw
		}
	}
	if !m.fits(csize, lsize) {
		return raw
	}
	return ZFSRecord{LSize: lsize, PSize: roundUp(csize, sector), Compressed: true}
}

// ZFSLogicalSize is the logical record size OpenZFS gives a file: a file smaller than the
// recordsize is ONE block of its own length rounded up to 512 bytes; larger files are cut
// into full records.
func ZFSLogicalSize(fileLen, recordsize int) int {
	if fileLen < recordsize {
		return roundUp(fileLen, 512)
	}
	return recordsize
}

// Zram models how zram stores one 4 KiB page (drivers/block/zram/zram_drv.c).
type Zram struct {
	PageSize int
	// HugeThreshold: a page compressing to at least this many bytes is stored uncompressed
	// (zsmalloc's huge_class_size, 3264 on x86-64 with 4 KiB pages).
	HugeThreshold int
	// ClassStep: zsmalloc size classes step (PAGE_SIZE >> CLASS_BITS = 16 bytes).
	ClassStep int
}

// DefaultZram is the x86-64 model.
var DefaultZram = Zram{PageSize: 4096, HugeThreshold: 3264, ClassStep: 16}

// SameFilled reports whether a page is one repeated machine word, which zram records as a
// flag and a value without compressing or allocating anything.
func SameFilled(page []byte) bool {
	if len(page) < 8 || len(page)%8 != 0 {
		return false
	}
	for i := 8; i < len(page); i += 8 {
		for j := 0; j < 8; j++ {
			if page[i+j] != page[j] {
				return false
			}
		}
	}
	return true
}

// Stored returns the bytes zram allocates for a page that compressed to csize (not called
// for same-filled pages, which allocate nothing).
func (z Zram) Stored(csize int) int {
	if csize >= z.HugeThreshold {
		return z.PageSize
	}
	return roundUp(csize, z.ClassStep)
}
