package main

// update-refind regenerates the rEFInd menu and the unified kernel images (UKIs) for every
// ostree deployment. It runs at shutdown from ostree-finalize-staged.service.
//
// The EFI system partition is small and every UKI embeds the whole initramfs (~350 MB), so
// space is the constraint this program is built around:
//
//   - the newest deployment gets every graphics mode, older deployments only Hybrid;
//   - UKIs are built one at a time, most important first, into "<name>.tmp" and renamed over
//     the old file only once complete, so a failure or a kill never leaves a half-written UKI
//     in place;
//   - before each build the free space is checked, and a UKI that does not fit is skipped
//     instead of filling the partition;
//   - UKIs that no longer belong, or that are smaller than their own kernel+initrd
//     (truncated by an earlier overflow), are removed;
//   - the menu only lists UKIs that exist and look complete, newest first, and selects the
//     newest by default.
//
// Failures are reported through the exit status; the old, working files are left alone.

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"text/template"
	"time"
)

const (
	refindConfigPath = "/boot/efi/EFI/refind/fedora-atomic.conf"
	bootEntriesPath  = "/boot/loader/entries"
	efiBasePath      = "/boot/efi/fedora-atomic"
	nvidiaMarker     = "/usr/share/nuclear/packages-added-nvidia"
	splashPath       = "/usr/share/backgrounds/artistic-landscape.bmp"

	// How many of the newest deployments get every graphics mode (the rest only Hybrid).
	fullModeEntries = 1
	// Keep this much of the ESP free when deciding whether a UKI fits.
	freeMargin = 64 << 20
	// Rough size of everything in a UKI besides the kernel, initrd and splash.
	ukiOverhead = 8 << 20
	// Stop starting new builds after this long; the unit's stop timeout is 5 minutes.
	buildBudget = 4 * time.Minute
)

// root is empty in production. Tests point it (UPDATE_REFIND_ROOT) at a scratch tree.
var root = os.Getenv("UPDATE_REFIND_ROOT")

func hostPath(p string) string { return root + p }

type bootEntry struct {
	file    string
	fields  map[string]string
	version int
}

func (e *bootEntry) title() string { return e.fields["title"] }

// dir is the directory holding this entry's UKIs, relative to the ESP root.
func (e *bootEntry) dir() string {
	return filepath.Join("/fedora-atomic", filepath.Dir(e.fields["linux"])[1:], strconv.Itoa(e.version))
}

func (e *bootEntry) ukiPath(mode string) string {
	return filepath.Join(hostPath("/boot/efi"), e.dir(), "UKI-"+mode+".efi")
}

// minUKISize is a lower bound for a complete UKI: it contains the kernel and the initrd.
func (e *bootEntry) minUKISize() int64 {
	return fileSize(hostPath("/boot"+e.fields["linux"])) + fileSize(hostPath("/boot"+e.fields["initrd"]))
}

func (e *bootEntry) estimate() int64 {
	return e.minUKISize() + fileSize(hostPath(splashPath)) + ukiOverhead
}

func fileSize(path string) int64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return st.Size()
}

// parseEntries reads the BLS entries and returns them newest first.
func parseEntries(dir string) ([]*bootEntry, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var entries []*bootEntry
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".conf") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			return nil, err
		}
		e := &bootEntry{file: f.Name(), fields: map[string]string{}}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			key, value, ok := strings.Cut(line, " ")
			if !ok {
				return nil, fmt.Errorf("malformed line in %s: %q", f.Name(), line)
			}
			e.fields[key] = value
		}
		for _, key := range []string{"title", "version", "linux", "initrd", "options"} {
			if _, ok := e.fields[key]; !ok {
				return nil, fmt.Errorf("missing key %q in %s", key, f.Name())
			}
		}
		v, err := strconv.Atoi(e.fields["version"])
		if err != nil {
			return nil, fmt.Errorf("non-numeric version in %s: %w", f.Name(), err)
		}
		e.version = v
		entries = append(entries, e)
	}
	// Highest BLS version is the newest deployment (what GRUB boots by default).
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].version != entries[j].version {
			return entries[i].version > entries[j].version
		}
		return entries[i].file > entries[j].file
	})
	return entries, nil
}

type ukiJob struct {
	entry *bootEntry
	mode  string
	must  bool // the machine has no working rEFInd entry without this one
}

// planJobs lists the UKIs to build, most important first: the newest deployment's Hybrid,
// then every other deployment's Hybrid (newest to oldest), then the extra graphics modes of
// the newest deployments. modes[0] must be "Hybrid".
func planJobs(entries []*bootEntry, modes []string, fullEntries int) []ukiJob {
	var jobs []ukiJob
	for i, e := range entries {
		jobs = append(jobs, ukiJob{entry: e, mode: modes[0], must: i == 0})
	}
	for i := 0; i < fullEntries && i < len(entries); i++ {
		for _, m := range modes[1:] {
			jobs = append(jobs, ukiJob{entry: entries[i], mode: m})
		}
	}
	return jobs
}

func dirSize(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// espFree returns the free bytes on the ESP. UPDATE_REFIND_ESP_BYTES lets tests simulate a
// small partition of that capacity.
func espFree(base string) (int64, error) {
	if capacity := os.Getenv("UPDATE_REFIND_ESP_BYTES"); capacity != "" {
		c, err := strconv.ParseInt(capacity, 10, 64)
		if err != nil {
			return 0, err
		}
		return c - dirSize(base), nil
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(base, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

var ukiConfigT = template.Must(template.New("uki").Parse(`[UKI]
Linux={{.Root}}/boot{{.Linux}}
Initrd={{.Root}}/boot/{{.Initrd}}
Uname={{.Uname}}
Cmdline={{.Options}} supergfxd.mode={{.Mode}}
OSRelease={{.OSRelease}}
Splash={{.Root}}/usr/share/backgrounds/artistic-landscape.bmp
SecureBootPrivateKey=/etc/pki/akmods/certs/nuclear.key
SecureBootCertificate=/etc/pki/akmods/certs/nuclear.crt
`))

func buildUKI(job ukiJob) error {
	e := job.entry
	final := e.ukiPath(job.mode)
	tmp := final + ".tmp"
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return err
	}

	split := strings.Split(e.fields["linux"], "/")
	var buf bytes.Buffer
	err := ukiConfigT.Execute(&buf, map[string]string{
		"Root":      root,
		"Linux":     e.fields["linux"],
		"Initrd":    e.fields["initrd"],
		"Uname":     split[len(split)-1],
		"Options":   e.fields["options"],
		"Mode":      job.mode,
		"OSRelease": "44", // TODO: not hard-coded
	})
	if err != nil {
		return err
	}

	cfg, err := os.CreateTemp("", "uki-*.conf")
	if err != nil {
		return err
	}
	defer os.Remove(cfg.Name())
	if _, err := cfg.Write(buf.Bytes()); err != nil {
		cfg.Close()
		return err
	}
	cfg.Close()

	ukify := os.Getenv("UPDATE_REFIND_UKIFY")
	if ukify == "" {
		ukify = "ukify"
	}
	cmd := exec.Command(ukify, "build", "--config", cfg.Name(), "--output", tmp)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("ukify: %w", err)
	}

	if got, want := fileSize(tmp), e.minUKISize(); got < want {
		os.Remove(tmp)
		return fmt.Errorf("built UKI is %d bytes, smaller than its kernel+initrd (%d): out of space?", got, want)
	}
	return os.Rename(tmp, final)
}

// prune removes leftover temp files, UKIs that are not part of the plan, and UKIs that are
// smaller than their own kernel+initrd (truncated), then empty directories.
func prune(base string, planned map[string]*bootEntry) {
	var dirs []string
	_ = filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != base {
				dirs = append(dirs, p)
			}
			return nil
		}
		switch {
		case strings.HasSuffix(p, ".tmp"):
			log.Printf("Removing leftover %s", p)
			os.Remove(p)
		case strings.HasSuffix(p, ".efi"):
			e, ok := planned[p]
			if !ok {
				log.Printf("Removing %s (not needed any more)", p)
				os.Remove(p)
			} else if fileSize(p) < e.minUKISize() {
				log.Printf("Removing %s (truncated: %d < %d bytes)", p, fileSize(p), e.minUKISize())
				os.Remove(p)
			}
		}
		return nil
	})
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, d := range dirs {
		os.Remove(d) // only succeeds when empty
	}
}

var menuLabels = map[string]string{
	"Vfio":       "Boot with VFIO",
	"Integrated": "Boot with only integrated GPU",
}

var menuT = template.Must(template.New("menu").Parse(`menuentry "{{.Title}}" {
	title "{{.Title}}"
	icon /EFI/refind/themes/rEFInd-glassy/icons/os_core.png
	loader {{.Dir}}/UKI-Hybrid.efi
	graphics on
{{range .Extra}}
	submenuentry "{{.Label}}" {
		loader {{$.Dir}}/UKI-{{.Mode}}.efi
		graphics on
	}
{{end}}}
`))

// renderConfig writes the rEFInd entries newest first, only for UKIs that exist, and makes
// the newest one the default.
func renderConfig(entries []*bootEntry, modes []string, exists func(string) bool) (string, error) {
	var buf bytes.Buffer
	first := true
	for _, e := range entries {
		if !exists(e.ukiPath("Hybrid")) {
			continue
		}
		if first {
			fmt.Fprintf(&buf, "default_selection %q\n\n", e.title())
			first = false
		}
		var extra []map[string]string
		for _, m := range modes[1:] {
			if exists(e.ukiPath(m)) {
				extra = append(extra, map[string]string{"Mode": m, "Label": menuLabels[m]})
			}
		}
		if err := menuT.Execute(&buf, map[string]any{"Title": e.title(), "Dir": e.dir(), "Extra": extra}); err != nil {
			return "", err
		}
		buf.WriteString("\n")
	}
	return buf.String(), nil
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func main() {
	start := time.Now()
	confPath := hostPath(refindConfigPath)
	if _, err := os.Stat(confPath); err != nil {
		log.Printf("Bailing out: %v", err)
		return
	}

	entries, err := parseEntries(hostPath(bootEntriesPath))
	if err != nil {
		log.Fatalf("Failed to read boot entries: %v", err)
	}
	if len(entries) == 0 {
		log.Fatalf("No boot entries in %s, leaving the rEFInd config alone", bootEntriesPath)
	}

	modes := []string{"Hybrid"}
	if _, err := os.Stat(hostPath(nvidiaMarker)); err == nil {
		modes = []string{"Hybrid", "Vfio", "Integrated"}
		log.Println("NVIDIA image: Vfio and Integrated UKIs for the newest deployment")
	} else {
		log.Println("Hybrid-only UKIs")
	}

	base := hostPath(efiBasePath)
	if err := os.MkdirAll(base, 0o755); err != nil {
		log.Fatalf("Failed to create %s: %v", base, err)
	}

	jobs := planJobs(entries, modes, fullModeEntries)
	planned := map[string]*bootEntry{}
	for _, j := range jobs {
		planned[j.entry.ukiPath(j.mode)] = j.entry
	}
	log.Printf("%d deployments, %d UKIs planned; newest first: %s", len(entries), len(jobs), entries[0].title())

	// Free space before building anything
	prune(base, planned)

	failed := false
	for _, j := range jobs {
		name := fmt.Sprintf("%s/UKI-%s", j.entry.title(), j.mode)

		if time.Since(start) > buildBudget && !j.must {
			log.Printf("Skipping %s: out of time (%s)", name, buildBudget)
			continue
		}
		free, err := espFree(base)
		if err != nil {
			log.Printf("Cannot read free space: %v", err)
			failed = true
			continue
		}
		// Replacing an existing file reuses its space once the rename happens, but the new
		// one is written first, so the whole size must fit next to the old one.
		if free-freeMargin < j.entry.estimate() {
			log.Printf("Skipping %s: needs ~%d MB, %d MB free on the ESP", name, j.entry.estimate()>>20, free>>20)
			if j.must {
				failed = true
			}
			continue
		}
		log.Printf("Building %s", name)
		if err := buildUKI(j); err != nil {
			log.Printf("FAILED %s: %v", name, err)
			failed = true
			continue
		}
	}

	// Anything still there but too small (e.g. a skipped, previously truncated UKI) goes
	prune(base, planned)

	text, err := renderConfig(entries, modes, func(p string) bool { return fileSize(p) > 0 })
	if err != nil {
		log.Fatalf("Failed to render the rEFInd config: %v", err)
	}
	if text == "" {
		log.Fatalf("No usable UKI for any deployment; leaving %s alone", refindConfigPath)
	}
	if err := writeFileAtomic(confPath, []byte(text)); err != nil {
		log.Fatalf("Failed writing rEFInd config %s: %v", confPath, err)
	}
	log.Printf("Updated rEFInd configuration at %s", refindConfigPath)

	if failed {
		log.Printf("Finished with errors after %s", time.Since(start).Round(time.Second))
		os.Exit(1)
	}
	log.Printf("Done in %s", time.Since(start).Round(time.Second))
}
