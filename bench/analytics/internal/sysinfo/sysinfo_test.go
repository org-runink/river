package sysinfo

import "testing"

func TestPickConfig(t *testing.T) {
	cfg := []byte("CONFIG_HZ=250\n# CONFIG_PREEMPT is not set\nCONFIG_DEFAULT_TCP_CONG=\"bbr\"\n")
	got := pickConfig(cfg, []string{"CONFIG_HZ", "CONFIG_PREEMPT", "CONFIG_DEFAULT_TCP_CONG", "CONFIG_MISSING"})
	want := map[string]string{"CONFIG_HZ": "250", "CONFIG_PREEMPT": "n", "CONFIG_DEFAULT_TCP_CONG": "bbr", "CONFIG_MISSING": "n"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestCollectReadsRunningKernel(t *testing.T) {
	in := Collect()
	if in.KernelRelease == "" || in.LogicalCPUs == 0 || in.MemTotalKiB == 0 {
		t.Fatalf("Collect returned an empty description: %+v", in)
	}
}

func TestRedactCmdline(t *testing.T) {
	in := "root=UUID=84827144-8ff0-4c72-8135-0fe1b51644c1 cryptdevice=UUID=84827144-8FF0-4c72-8135-0fe1b51644c1:luks-84827144-8ff0-4c72-8135-0fe1b51644c1 rw"
	want := "root=UUID=<uuid> cryptdevice=UUID=<uuid>:luks-<uuid> rw"
	if got := RedactCmdline(in); got != want {
		t.Fatalf("got %q", got)
	}
}
