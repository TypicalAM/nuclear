package main

// Run with:  go test build-ctx/refind-updater.go build-ctx/refind-updater_test.go
//
// The end-to-end tests run the real main() in a subprocess against a scratch tree
// (UPDATE_REFIND_ROOT), a fake ukify, and a simulated small ESP (UPDATE_REFIND_ESP_BYTES).

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	if os.Getenv("UPDATE_REFIND_RUN_MAIN") == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}

const (
	mb         = 1 << 20
	kernelSize = 1 * mb
	initrdSize = 10 * mb
	splashSize = 1 * mb
	// the fake ukify writes kernel + initrd + 1 MB
	ukiSize = kernelSize + initrdSize + 1*mb
)

func title(v int) string { return fmt.Sprintf("Fedora Linux 44 (Nuclear) (ostree:%d)", 4-v) }
func csum(v int) string  { return fmt.Sprintf("csum%d", v) }

// newTree builds a scratch root with BLS entries of versions 1..n (n newest).
func newTree(t *testing.T, n int, nvidia bool) string {
	t.Helper()
	r := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	write := func(path string, size int) {
		must(os.MkdirAll(filepath.Dir(path), 0o755))
		must(os.WriteFile(path, make([]byte, size), 0o644))
	}
	write(filepath.Join(r, "boot/efi/EFI/refind/fedora-atomic.conf"), 0)
	write(filepath.Join(r, "usr/share/backgrounds/artistic-landscape.bmp"), splashSize)
	if nvidia {
		write(filepath.Join(r, "usr/share/nuclear/packages-added-nvidia"), 0)
	}
	for v := 1; v <= n; v++ {
		write(filepath.Join(r, "boot/ostree/fedora-"+csum(v)+"/vmlinuz-6.19"), kernelSize)
		write(filepath.Join(r, "boot/ostree/fedora-"+csum(v)+"/initramfs-6.19.img"), initrdSize)
		bls := fmt.Sprintf("title %s\nversion %d\nlinux /ostree/fedora-%s/vmlinuz-6.19\ninitrd /ostree/fedora-%s/initramfs-6.19.img\noptions root=UUID=x ostree=/ostree/boot.0/fedora/%s/0\n",
			title(v), v, csum(v), csum(v), csum(v))
		must(os.MkdirAll(filepath.Join(r, "boot/loader/entries"), 0o755))
		must(os.WriteFile(filepath.Join(r, fmt.Sprintf("boot/loader/entries/ostree-%d.conf", v)), []byte(bls), 0o644))
	}
	return r
}

func fakeUkify(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "ukify")
	script := `#!/usr/bin/bash
# fake ukify: build --config C --output O
cfg=$3; out=$5
linux=$(sed -n 's/^Linux=//p' "$cfg"); initrd=$(sed -n 's/^Initrd=//p' "$cfg")
size=$(( $(stat -c %s "$linux") + $(stat -c %s "$initrd") + 1048576 ))
if [ -n "$UKIFY_FAIL_MATCH" ] && grep -q "$UKIFY_FAIL_MATCH" "$cfg"; then
  head -c 1048576 /dev/zero > "$out"   # a partial write, like an ESP running out of space
  exit 1
fi
head -c "$size" /dev/zero > "$out"
`
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

type result struct {
	root   string
	exit   int
	output string
}

func run(t *testing.T, root string, espBytes int, env ...string) result {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(),
		"UPDATE_REFIND_RUN_MAIN=1",
		"UPDATE_REFIND_ROOT="+root,
		"UPDATE_REFIND_UKIFY="+fakeUkify(t),
		fmt.Sprintf("UPDATE_REFIND_ESP_BYTES=%d", espBytes),
	)
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return result{root: root, exit: code, output: string(out)}
}

func (r result) uki(v int, mode string) string {
	return filepath.Join(r.root, "boot/efi/fedora-atomic/ostree/fedora-"+csum(v), fmt.Sprint(v), "UKI-"+mode+".efi")
}

func (r result) size(path string) int64 { return fileSize(path) }

func (r result) conf(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(r.root, "boot/efi/EFI/refind/fedora-atomic.conf"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (r result) leftovers(t *testing.T) []string {
	t.Helper()
	var l []string
	_ = filepath.WalkDir(filepath.Join(r.root, "boot/efi"), func(p string, d os.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(p, ".tmp") {
			l = append(l, p)
		}
		return nil
	})
	return l
}

func plant(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---- unit tests ----

func TestParseEntriesNewestFirst(t *testing.T) {
	r := newTree(t, 3, true)
	es, err := parseEntries(filepath.Join(r, "boot/loader/entries"))
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 3 || es[0].version != 3 || es[1].version != 2 || es[2].version != 1 {
		t.Fatalf("not newest first: %v %v %v", es[0].version, es[1].version, es[2].version)
	}
}

func TestPlanJobsPriority(t *testing.T) {
	r := newTree(t, 4, true)
	es, _ := parseEntries(filepath.Join(r, "boot/loader/entries"))
	jobs := planJobs(es, []string{"Hybrid", "Vfio", "Integrated"}, 1)
	var got []string
	for _, j := range jobs {
		got = append(got, fmt.Sprintf("%d/%s", j.entry.version, j.mode))
	}
	want := "4/Hybrid 3/Hybrid 2/Hybrid 1/Hybrid 4/Vfio 4/Integrated"
	if strings.Join(got, " ") != want {
		t.Fatalf("plan = %v, want %s", got, want)
	}
	if !jobs[0].must || jobs[1].must {
		t.Fatal("only the newest Hybrid is required")
	}
	if jobs := planJobs(es, []string{"Hybrid"}, 1); len(jobs) != 4 {
		t.Fatalf("hybrid-only plan has %d jobs, want 4", len(jobs))
	}
}

func TestRenderConfig(t *testing.T) {
	r := newTree(t, 3, true)
	es, _ := parseEntries(filepath.Join(r, "boot/loader/entries"))
	modes := []string{"Hybrid", "Vfio", "Integrated"}
	have := map[string]bool{
		es[1].ukiPath("Hybrid"): true, // newest has no UKI at all
		es[1].ukiPath("Vfio"):   true,
		es[2].ukiPath("Hybrid"): true,
	}
	text, err := renderConfig(es, modes, func(p string) bool { return have[p] })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(text, fmt.Sprintf("default_selection %q", es[1].title())) {
		t.Fatalf("default must be the newest entry that has a UKI:\n%s", text)
	}
	if strings.Contains(text, es[0].title()+`" {`) {
		t.Fatal("entry without a UKI must not be listed")
	}
	if !strings.Contains(text, "Boot with VFIO") || strings.Contains(text, "integrated GPU") {
		t.Fatalf("submenus must only exist for UKIs that exist:\n%s", text)
	}
	if strings.Index(text, es[1].title()+`" {`) > strings.Index(text, es[2].title()+`" {`) {
		t.Fatal("entries must be newest first")
	}
}

// ---- end-to-end ----

func TestEndToEndFitsAndCleansUp(t *testing.T) {
	r := newTree(t, 4, true)
	// leftovers from earlier runs: a deployment that is gone, an old extra mode that is no
	// longer planned, a truncated UKI that is planned, and a temp file from a killed build
	plant(t, filepath.Join(r, "boot/efi/fedora-atomic/ostree/fedora-gone/9/UKI-Hybrid.efi"), 5*mb)
	plant(t, filepath.Join(r, "boot/efi/fedora-atomic/ostree/fedora-csum2/2/UKI-Vfio.efi"), ukiSize)
	plant(t, filepath.Join(r, "boot/efi/fedora-atomic/ostree/fedora-csum3/3/UKI-Hybrid.efi"), 3*mb)
	plant(t, filepath.Join(r, "boot/efi/fedora-atomic/ostree/fedora-csum4/4/UKI-Vfio.efi.tmp"), 7*mb)

	res := run(t, r, 64*mb+6*ukiSize+40*mb)
	if res.exit != 0 {
		t.Fatalf("exit %d\n%s", res.exit, res.output)
	}
	for _, c := range []struct {
		v    int
		mode string
	}{{4, "Hybrid"}, {3, "Hybrid"}, {2, "Hybrid"}, {1, "Hybrid"}, {4, "Vfio"}, {4, "Integrated"}} {
		if got := res.size(res.uki(c.v, c.mode)); got != ukiSize {
			t.Errorf("UKI %d/%s is %d bytes, want %d", c.v, c.mode, got, ukiSize)
		}
	}
	for _, gone := range []string{
		filepath.Join(r, "boot/efi/fedora-atomic/ostree/fedora-gone"),
		res.uki(2, "Vfio"), res.uki(3, "Vfio"), res.uki(1, "Vfio"),
	} {
		if _, err := os.Stat(gone); err == nil {
			t.Errorf("%s should have been removed", gone)
		}
	}
	if l := res.leftovers(t); len(l) != 0 {
		t.Errorf("temp files left behind: %v", l)
	}
	conf := res.conf(t)
	if !strings.HasPrefix(conf, fmt.Sprintf("default_selection %q", title(4))) {
		t.Errorf("default is not the newest entry:\n%s", conf)
	}
	if strings.Count(conf, "\nmenuentry ") != 4 || strings.Count(conf, "\n\tsubmenuentry ") != 2 {
		t.Errorf("want 4 entries and 2 submenus (newest only):\n%s", conf)
	}
	if strings.Index(conf, title(4)+`" {`) > strings.Index(conf, title(1)+`" {`) {
		t.Error("entries are not newest first")
	}
}

func TestEndToEndSkipsWhatDoesNotFit(t *testing.T) {
	r := newTree(t, 4, true)
	// a truncated UKI for a planned job that will be skipped must still be removed
	plant(t, filepath.Join(r, "boot/efi/fedora-atomic/ostree/fedora-csum1/1/UKI-Hybrid.efi"), 2*mb)

	// margin (64) + room for exactly three builds: the fourth needs 20 MB more than is left
	res := run(t, r, 64*mb+20*mb+2*ukiSize+1)
	if res.exit != 0 {
		t.Fatalf("a skipped lower-priority UKI is not an error, got exit %d\n%s", res.exit, res.output)
	}
	for v := 4; v >= 2; v-- {
		if res.size(res.uki(v, "Hybrid")) != ukiSize {
			t.Errorf("entry %d should have been built\n%s", v, res.output)
		}
	}
	if res.size(res.uki(1, "Hybrid")) != 0 || res.size(res.uki(4, "Vfio")) != 0 {
		t.Errorf("lower-priority UKIs must be skipped, and the truncated one removed\n%s", res.output)
	}
	conf := res.conf(t)
	if strings.Contains(conf, title(1)) || strings.Contains(conf, "submenuentry") {
		t.Errorf("skipped UKIs must not be in the menu:\n%s", conf)
	}
	if !strings.Contains(res.output, "Skipping") {
		t.Errorf("the skip must be logged:\n%s", res.output)
	}
}

func TestEndToEndFailedBuildKeepsOldAndFailsLoudly(t *testing.T) {
	r := newTree(t, 3, true)
	// the newest entry already has a complete UKI from a previous run; its rebuild fails
	plant(t, filepath.Join(r, "boot/efi/fedora-atomic/ostree/fedora-csum3/3/UKI-Hybrid.efi"), ukiSize+5*mb)

	res := run(t, r, 1024*mb, "UKIFY_FAIL_MATCH="+csum(3))
	if res.exit != 1 {
		t.Fatalf("a failed build must exit 1, got %d\n%s", res.exit, res.output)
	}
	if got := res.size(res.uki(3, "Hybrid")); got != int64(ukiSize+5*mb) {
		t.Errorf("the old complete UKI must be untouched, size is now %d", got)
	}
	if l := res.leftovers(t); len(l) != 0 {
		t.Errorf("the partial write must not be left behind: %v", l)
	}
	if res.size(res.uki(2, "Hybrid")) != ukiSize || res.size(res.uki(1, "Hybrid")) != ukiSize {
		t.Errorf("one failure must not stop the other entries\n%s", res.output)
	}
	if !strings.HasPrefix(res.conf(t), fmt.Sprintf("default_selection %q", title(3))) {
		t.Errorf("the entry with the surviving UKI is still the default:\n%s", res.conf(t))
	}
}

func TestEndToEndFailedNewestWithNoOldCopy(t *testing.T) {
	r := newTree(t, 3, true)
	res := run(t, r, 1024*mb, "UKIFY_FAIL_MATCH="+csum(3))
	if res.exit != 1 {
		t.Fatalf("exit %d\n%s", res.exit, res.output)
	}
	conf := res.conf(t)
	if strings.Contains(conf, title(3)+`" {`) {
		t.Errorf("an entry without a UKI must not be listed:\n%s", conf)
	}
	if !strings.HasPrefix(conf, fmt.Sprintf("default_selection %q", title(2))) {
		t.Errorf("default must fall back to the newest entry that exists:\n%s", conf)
	}
}

func TestEndToEndNoRefindConfigBailsOut(t *testing.T) {
	r := newTree(t, 2, false)
	_ = os.Remove(filepath.Join(r, "boot/efi/EFI/refind/fedora-atomic.conf"))
	res := run(t, r, 1024*mb)
	if res.exit != 0 || !strings.Contains(res.output, "Bailing out") {
		t.Fatalf("exit %d\n%s", res.exit, res.output)
	}
	if _, err := os.Stat(filepath.Join(r, "boot/efi/fedora-atomic")); err == nil {
		t.Error("must not touch the ESP when rEFInd is not set up")
	}
}

func TestEndToEndHybridOnlyWithoutNvidia(t *testing.T) {
	r := newTree(t, 3, false)
	res := run(t, r, 1024*mb)
	if res.exit != 0 {
		t.Fatalf("exit %d\n%s", res.exit, res.output)
	}
	if strings.Contains(res.conf(t), "submenuentry") {
		t.Error("non-NVIDIA image has no extra graphics modes")
	}
	if res.size(res.uki(3, "Vfio")) != 0 {
		t.Error("no Vfio UKI without NVIDIA")
	}
}
