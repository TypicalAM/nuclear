package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

type (
	BootEntries       map[string]BootEntry
	BootEntry         map[KernelConfigKey]KernelConfigValue
	KernelConfigKey   string
	KernelConfigValue string
)

func makeKey(indent int, key KernelConfigKey, val KernelConfigValue) string {
	prefix := strings.Repeat("\t", indent)
	if strings.ContainsRune(string(val), ' ') {
		return fmt.Sprintf("%s%s \"%s\"\n", prefix, key, val)
	}
	return fmt.Sprintf("%s%s %s\n", prefix, key, val)
}

func generateEntryText(entry BootEntry) string {
	b := strings.Builder{}
	title := entry["title"]
	linux := entry["linux"]
	initrd := entry["initrd"]
	opts := entry["options"]

	b.WriteString(fmt.Sprintf("menuentry \"%s\" {\n", title))
	b.WriteString(makeKey(1, "title", title))
	b.WriteString(makeKey(1, "loader", "/fedora-atomic"+linux))
	b.WriteString(makeKey(1, "initrd", "/fedora-atomic"+initrd))
	b.WriteString(makeKey(1, "options", opts))
	b.WriteString(makeKey(1, "graphics", "on"))
	b.WriteString(makeKey(1, "icon", "/EFI/refind/themes/rEFInd-glassy/icons/os_chakra.png"))

	vfioOpts := opts + " supergfxd.mode=Vfio"
	b.WriteString("\n\tsubmenuentry \"Boot with VFIO\" {\n")
	b.WriteString(makeKey(2, "loader", "/fedora-atomic"+linux))
	b.WriteString(makeKey(2, "initrd", "/fedora-atomic"+initrd))
	b.WriteString(makeKey(2, "options", KernelConfigValue(vfioOpts)))
	b.WriteString("\t}\n")

	iGPUOpts := opts + " supergfxd.mode=Integrated"
	b.WriteString("\n\tsubmenuentry \"Boot with only integrated GPU\" {\n")
	b.WriteString(makeKey(2, "loader", "/fedora-atomic"+linux))
	b.WriteString(makeKey(2, "initrd", "/fedora-atomic"+initrd))
	b.WriteString(makeKey(2, "options", KernelConfigValue(iGPUOpts)))
	b.WriteString("\t}\n")

	b.WriteString("}\n")
	return b.String()
}

func mustGet(entry BootEntry, key string, file string) KernelConfigValue {
	val, ok := entry[KernelConfigKey(key)]
	if !ok {
		log.Fatalf("Missing key %q in entry %q", key, file)
	}
	return val
}

func main() {
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
	}

	// Generate refind config
	var buf bytes.Buffer
	for _, entry := range bootEntries {
		text := generateEntryText(entry)
		buf.WriteString(text)
		buf.WriteRune('\n')
	}

	refindConfig := "/boot/efi/EFI/refind/fedora-atomic.conf"
	if err := os.WriteFile(refindConfig, buf.Bytes(), 0644); err != nil {
		log.Fatalf("Failed writing rEFInd config %q: %v", refindConfig, err)
	}

	log.Printf("Updated rEFInd configuration at %s", refindConfig)
}
