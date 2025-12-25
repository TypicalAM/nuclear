package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

const REFIND_CONFIG_PATH = "/boot/efi/EFI/refind/fedora-atomic.conf"

type (
	BootEntries       map[string]BootEntry
	BootEntry         map[KernelConfigKey]KernelConfigValue
	KernelConfigKey   string
	KernelConfigValue string
)

var generateEntryT = template.Must(template.New("entry").Funcs(template.FuncMap{
	"repeat":   strings.Repeat,
	"hasSpace": func(s string) bool { return strings.ContainsRune(s, ' ') },
}).Parse(
	`menuentry "{{.Title}}" {
	title "{{.Title}}"
	icon /EFI/refind/themes/rEFInd-glassy/icons/os_core.png
	loader {{.UkiPath}}
	graphics on

	submenuentry "Boot with VFIO" {
		loader /fedora-atomic{{.Linux}}
		initrd /fedora-atomic{{.Initrd}}
		options "{{.Options}} supergfxd.mode=Vfio"
		graphics on
	}

	submenuentry "Boot with only integrated GPU" {
		loader /fedora-atomic{{.Linux}}
		initrd /fedora-atomic{{.Initrd}}
		options "{{.Options}} supergfxd.mode=Integrated"
		graphics on
	}
}`))

// GenerateEntry renders the entry from a data map.
func GenerateEntry(entry BootEntry) (string, error) {
	title := string(entry["title"])
	linux := string(entry["linux"])
	initrd := string(entry["initrd"])
	opts := string(entry["options"])
	data := map[string]string{
		"Title":   title,
		"Linux":   linux,
		"Initrd":  initrd,
		"Options": opts,
		"UkiPath": filepath.Dir("/fedora-atomic"+linux) + "/UKI.efi",
	}
	var buf bytes.Buffer
	if err := generateEntryT.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

const ukiTemplate = `[UKI]
Linux=/boot/efi/fedora-atomic%[1]s
Initrd=/boot/efi/fedora-atomic%[2]s
Uname=%[3]s
Cmdline=%[4]s
OSRelease=%[5]s`

func generateUKI(entry BootEntry, dst string) error {
	split := strings.Split(string(entry["linux"]), "/")
	uname := split[len(split)-1]
	cfg := fmt.Sprintf(ukiTemplate, entry["linux"], entry["initrd"], uname, entry["options"], "43") // TODO: Not hardcode this

	tmp, err := os.CreateTemp("", "uki-*.conf")
	if err != nil {
		return fmt.Errorf("create config tmp: %w", err)
	}
	cfgPath := tmp.Name()
	defer os.Remove(cfgPath)

	if _, err := tmp.WriteString(cfg); err != nil {
		tmp.Close()
		return fmt.Errorf("write config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close config: %w", err)
	}

	// Build UKI
	cmd := exec.Command("ukify", "build", "--config", cfgPath, "--output", dst)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ukify build: %w", err)
	}

	return nil
}

func mustGet(entry BootEntry, key string, file string) KernelConfigValue {
	val, ok := entry[KernelConfigKey(key)]
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
			newEntry[KernelConfigKey(key)] = KernelConfigValue(value)
		}

		bootEntries[dirEntry.Name()] = newEntry
	}

	// Copy kernel + initrd to EFI
	for name, entry := range bootEntries {
		log.Printf("Processing entry: %s", name)

		linux := mustGet(entry, "linux", name)
		initrd := mustGet(entry, "initrd", name)

		for _, item := range []KernelConfigValue{linux, initrd} {
			src := filepath.Join("/boot", string(item)[1:])
			dst := filepath.Join("/boot/efi/fedora-atomic", string(item)[1:])

			data, err := os.ReadFile(src)
			if err != nil {
				log.Fatalf("Failed reading %q: %v", src, err)
			}

			if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
				log.Fatalf("Failed creating directory for %q: %v", dst, err)
			}

			if err := os.WriteFile(dst, data, 0600); err != nil {
				log.Fatalf("Failed writing %q: %v", dst, err)
			}

			log.Printf("Copied %s → %s", src, dst)
		}

		ukiDst := filepath.Join("/boot/efi/fedora-atomic", filepath.Dir(string(linux)), "/UKI.efi")
		if err := generateUKI(entry, ukiDst); err != nil {
			log.Fatalf("Failed to generate UKI: %v", err)
		}

		log.Printf("Generated UKI at %s", ukiDst)
	}

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
