package compress

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestUringReadsEveryExtent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	data := make([]byte, 3<<20+123)
	for i := range data {
		data[i] = byte(i * 7)
	}
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	fd, err := syscall.Open(p, syscall.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)
	for _, test := range []string{"seq", "rowgroup"} {
		ex := ioExtents(int64(len(data)), test, 64<<10)
		var want int64
		for _, e := range ex {
			want += e[1]
		}
		for _, qd := range []int{1, 4, 32} {
			got, err := uringRead(fd, ex, 64<<10, qd)
			if errors.Is(err, syscall.ENOSYS) || errors.Is(err, syscall.EPERM) {
				t.Skipf("io_uring unavailable: %v", err)
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("%s qd=%d: read %d bytes, want %d", test, qd, got, want)
			}
		}
	}
}
