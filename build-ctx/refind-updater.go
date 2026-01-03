package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"text/template"
)

const REFIND_CONFIG_PATH = "/boot/efi/EFI/refind/fedora-atomic.conf"

type (
	BootEntries map[string]BootEntry
	BootEntry   map[string]string
)

var generateEntryT = template.Must(template.New("entry").Funcs(template.FuncMap{
	"repeat":   strings.Repeat,
	"hasSpace": func(s string) bool { return strings.ContainsRune(s, ' ') },
}).Parse(
	`menuentry "{{.Title}}" {
	title "{{.Title}}"
	icon /EFI/refind/themes/rEFInd-glassy/icons/os_core.png
	loader {{.UkiDir}}/UKI-Hybrid.efi
	graphics on

	submenuentry "Boot with VFIO" {
		loader {{.UkiDir}}/UKI-Vfio.efi
		graphics on
	}

	submenuentry "Boot with only integrated GPU" {
		loader {{.UkiDir}}/UKI-Integrated.efi
		graphics on
	}
}`))

// GenerateEntry renders the entry from a data map.
func GenerateEntry(entry BootEntry) (string, error) {
	data := map[string]string{
		"Title":   entry["title"],
		"Options": entry["options"],
		"UkiDir":  filepath.Join("/fedora-atomic", filepath.Dir(entry["linux"])[1:], entry["version"]),
	}
	var buf bytes.Buffer
	if err := generateEntryT.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

var ukiTemplateT = template.Must(template.New("uki").Funcs(template.FuncMap{
	"repeat":   strings.Repeat,
	"hasSpace": func(s string) bool { return strings.ContainsRune(s, ' ') },
}).Parse(
	`[UKI]
Linux=/boot{{.Linux}}
Initrd=/boot/{{.Initrd}}
Uname={{.Uname}}
Cmdline={{.Options}} supergfxd.mode={{.GraphicsMode}}
OSRelease={{.OSRelease}}
Splash=/ctx/artistic-landscape.bmp
`))

func generateUKI(entry BootEntry, dstDirectory string) error {
	split := strings.Split(entry["linux"], "/")
	uname := split[len(split)-1]

	graphicsModes := []string{"Hybrid", "Vfio", "Integrated"}

	var wg sync.WaitGroup
	errCh := make(chan error, len(graphicsModes))

	for _, mode := range graphicsModes {
		wg.Go(func() {

			data := map[string]string{
				"Linux":        entry["linux"],
				"Initrd":       entry["initrd"],
				"Uname":        uname,
				"Options":      entry["options"],
				"GraphicsMode": mode,
				"OSRelease":    "43", // TODO: not hard-coded
			}

			var buf bytes.Buffer
			if err := ukiTemplateT.Execute(&buf, data); err != nil {
				errCh <- fmt.Errorf("template exec (%s): %w", mode, err)
				return
			}

			cfg, err := os.CreateTemp("", "uki-*.conf")
			if err != nil {
				errCh <- fmt.Errorf("create temp config (%s): %w", mode, err)
				return
			}
			// defer os.Remove(cfg.Name())

			if _, err := cfg.Write(buf.Bytes()); err != nil {
				cfg.Close()
				errCh <- fmt.Errorf("write config (%s): %w", mode, err)
				return
			}
			cfg.Close()

			dst := filepath.Join(dstDirectory, fmt.Sprintf("UKI-%s.efi", mode))
			cmd := exec.Command("ukify", "build", "--config", cfg.Name(), "--output", dst)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				errCh <- fmt.Errorf("ukify build (%s): %w", mode, err)
				return
			}
		})
	}

	wg.Wait()
	close(errCh)

	// return the first error we hit (if any)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

func mustGet(entry BootEntry, key string, file string) string {
	val, ok := entry[key]
	if !ok {
		log.Fatalf("Missing key %q in entry %q", key, file)
	}
	return val
}

func main() {
	if _, err := os.Stat(REFIND_CONFIG_PATH); err != nil {
		log.Printf("Bailing out: %v\n", err)
		return
	}

	bootEntriesBasePath := "/boot/loader/entries/"
	entries, err := os.ReadDir(bootEntriesBasePath)
	if err != nil {
		log.Fatalf("Failed to read entries directory %q: %v", bootEntriesBasePath, err)
	}

	efiBase := "/boot/efi/fedora-atomic"
	log.Printf("Clearing old EFI kernel/initrd files in %s", efiBase)
	if err := os.RemoveAll(efiBase); err != nil {
		log.Fatalf("Failed to clean %q: %v", efiBase, err)
	}

	if err := os.MkdirAll(efiBase, 0755); err != nil {
		log.Fatalf("Failed to recreate %q: %v", efiBase, err)
	}

	log.Printf("Cleared and recreated %s", efiBase)

	bootEntries := make(BootEntries)

	// Parse entries
	for _, dirEntry := range entries {
		filename := filepath.Join(bootEntriesBasePath, dirEntry.Name())
		data, err := os.ReadFile(filename)
		if err != nil {
			log.Fatalf("Failed to read entry %q: %v", filename, err)
		}

		newEntry := make(BootEntry)
		for line := range strings.SplitSeq(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			key, value, ok := strings.Cut(line, " ")
			if !ok {
				log.Fatalf("Malformed line in %q: %q", filename, line)
			}
			newEntry[key] = value
		}

		bootEntries[dirEntry.Name()] = newEntry
	}

	type job struct {
		name  string
		entry BootEntry
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(bootEntries))

	for name, entry := range bootEntries {
		wg.Add(1)
		go func(j job) {
			defer wg.Done()

			log.Printf("Processing entry: %s", j.name)

			linux := mustGet(j.entry, "linux", j.name)
			dst := filepath.Join("/boot/efi/fedora-atomic", filepath.Dir(linux)[1:], j.entry["version"])
			if err := os.MkdirAll(dst, 0755); err != nil {
				errCh <- fmt.Errorf("mkdir %q: %w", dst, err)
				return
			}

			if err := generateUKI(j.entry, dst); err != nil {
				errCh <- fmt.Errorf("generateUKI %q: %w", j.name, err)
				return
			}
			log.Printf("Generated UKIs at %s", dst)
		}(job{name: name, entry: entry})
	}

	wg.Wait()
	close(errCh)

	// Generate refind config
	var buf bytes.Buffer
	for _, entry := range bootEntries {
		text, err := GenerateEntry(entry)
		if err != nil {
			log.Fatalf("Failed to generate entry text: %v", err)
		}

		buf.WriteString(text)
		buf.WriteRune('\n')
	}

	if err := os.WriteFile(REFIND_CONFIG_PATH, buf.Bytes(), 0644); err != nil {
		log.Fatalf("Failed writing rEFInd config %s: %v", REFIND_CONFIG_PATH, err)
	}

	log.Printf("Updated rEFInd configuration at %s", REFIND_CONFIG_PATH)
}
