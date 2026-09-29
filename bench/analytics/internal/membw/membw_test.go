package membw

import "testing"

func TestRunComputesStreamKernels(t *testing.T) {
	for _, madv := range []bool{false, true} {
		o := Options{Elements: 1 << 16, Workers: 3, Madvise: madv}
		ar, err := Alloc(o)
		if err != nil {
			t.Fatal(err)
		}
		r := Run(o, ar)
		// After one pass from a=1, b=2, c=0: c=a=1; b=3c=3; c=a+b=4; a=b+3c=15.
		if ar.a[7] != 15 || ar.b[7] != 3 || ar.c[7] != 4 {
			t.Fatalf("madv=%v: a,b,c = %v,%v,%v", madv, ar.a[7], ar.b[7], ar.c[7])
		}
		if r.Copy <= 0 || r.Scale <= 0 || r.Add <= 0 || r.Triad <= 0 {
			t.Fatalf("madv=%v: non-positive bandwidth %+v", madv, r)
		}
		ar.Free()
	}
}
