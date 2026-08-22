package c

import (
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const maxDylibBytes = 100 << 20 // 100 MiB

// dylibInfo is one shared library found on disk.
type dylibInfo struct {
	Abs     string
	Rel     string
	Exports []string
}

func findDylibs(root string, files []walkFile) []dylibInfo {
	seen := map[string]bool{}
	var out []dylibInfo
	add := func(abs, rel string) {
		if abs == "" || seen[abs] {
			return
		}
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() || info.Size() > maxDylibBytes {
			return
		}
		syms, err := exportedSymbols(abs)
		if err != nil || len(syms) == 0 {
			return
		}
		seen[abs] = true
		if rel == "" {
			if r, err := filepath.Rel(root, abs); err == nil {
				rel = filepath.ToSlash(r)
			} else {
				rel = filepath.Base(abs)
			}
		}
		out = append(out, dylibInfo{Abs: abs, Rel: rel, Exports: syms})
	}
	for _, f := range files {
		if f.Lang == "dylib" {
			add(f.Abs, f.Rel)
		}
	}
	// Compiled libs often live under build/ (ignored for source). Hunt them
	// so unused dynamic exports are still visible.
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if skipDylibDir(name) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isDylibName(name) {
			return nil
		}
		add(path, "")
		return nil
	})
	return out
}

func skipDylibDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", "node_modules", ".venv", "__pycache__",
		".dSYM", "vendor":
		return true
	}
	return strings.HasPrefix(name, ".") && name != "."
}

func isDylibName(name string) bool {
	low := strings.ToLower(name)
	if strings.HasSuffix(low, ".dylib") || strings.HasSuffix(low, ".dll") || strings.HasSuffix(low, ".so") {
		return true
	}
	i := strings.Index(low, ".so.")
	if i < 0 {
		return false
	}
	ver := low[i+4:]
	if ver == "" {
		return false
	}
	for _, r := range ver {
		if r != '.' && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// walkFile is the subset of walk.File used by findDylibs so dylib.go
// does not import walk in a cycle. Detect passes walk.File values.
type walkFile struct {
	Abs, Rel, Lang string
}

func exportedSymbols(path string) ([]string, error) {
	if syms, err := elfExports(path); err == nil && len(syms) > 0 {
		return syms, nil
	}
	if syms, err := machoExports(path); err == nil && len(syms) > 0 {
		return syms, nil
	}
	if syms, err := peExports(path); err == nil && len(syms) > 0 {
		return syms, nil
	}
	return nil, nil
}

func elfExports(path string) ([]string, error) {
	f, err := elf.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var raw []elf.Symbol
	if dyn, err := f.DynamicSymbols(); err == nil {
		raw = dyn
	}
	if len(raw) == 0 {
		if all, err := f.Symbols(); err == nil {
			raw = all
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, s := range raw {
		if s.Name == "" || s.Section == elf.SHN_UNDEF {
			continue
		}
		bind := elf.ST_BIND(s.Info)
		if bind != elf.STB_GLOBAL && bind != elf.STB_WEAK {
			continue
		}
		typ := elf.ST_TYPE(s.Info)
		if typ != elf.STT_FUNC && typ != elf.STT_OBJECT && typ != elf.STT_NOTYPE {
			continue
		}
		name := stripSymVer(s.Name)
		name = cName(name)
		if name == "" || seen[name] || reservedExport(name) {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out, nil
}

func machoExports(path string) ([]string, error) {
	f, err := macho.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if f.Symtab == nil {
		return nil, nil
	}
	const (
		nExt  = 0x01
		nType = 0x0e
		nSect = 0x0e
	)
	var out []string
	seen := map[string]bool{}
	for _, s := range f.Symtab.Syms {
		if s.Name == "" {
			continue
		}
		if s.Type&nExt == 0 {
			continue
		}
		if s.Type&nType != nSect {
			continue
		}
		name := cName(s.Name)
		if name == "" || seen[name] || reservedExport(name) {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out, nil
}

func peExports(path string) ([]string, error) {
	f, err := pe.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// debug/pe does not expose a high-level export iterator on all Go versions.
	// Prefer the export-directory symbols when present.
	if f.OptionalHeader == nil {
		return nil, nil
	}
	var dd []pe.DataDirectory
	switch oh := f.OptionalHeader.(type) {
	case *pe.OptionalHeader32:
		dd = oh.DataDirectory[:]
	case *pe.OptionalHeader64:
		dd = oh.DataDirectory[:]
	}
	if len(dd) <= int(pe.IMAGE_DIRECTORY_ENTRY_EXPORT) {
		return nil, nil
	}
	ed := dd[pe.IMAGE_DIRECTORY_ENTRY_EXPORT]
	if ed.VirtualAddress == 0 || ed.Size < 40 {
		return nil, nil
	}
	data, err := readPEVA(f, ed.VirtualAddress, ed.Size)
	if err != nil || len(data) < 40 {
		return nil, err
	}
	// IMAGE_EXPORT_DIRECTORY: NumberOfNames at 24, AddressOfNames at 32.
	nNames := le32(data[24:])
	addrNames := le32(data[32:])
	if nNames == 0 || nNames > 100000 {
		return nil, nil
	}
	nameTable, err := readPEVA(f, addrNames, nNames*4)
	if err != nil {
		return nil, err
	}
	var out []string
	seen := map[string]bool{}
	for i := uint32(0); i < nNames; i++ {
		off := i * 4
		if int(off+4) > len(nameTable) {
			break
		}
		rva := le32(nameTable[off:])
		s, err := readPEZString(f, rva)
		if err != nil || s == "" {
			continue
		}
		name := cName(s)
		if name == "" || seen[name] || reservedExport(name) {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out, nil
}

func readPEVA(f *pe.File, va, size uint32) ([]byte, error) {
	for _, sec := range f.Sections {
		if va >= sec.VirtualAddress && va < sec.VirtualAddress+sec.VirtualSize {
			off := va - sec.VirtualAddress
			b, err := sec.Data()
			if err != nil {
				return nil, err
			}
			if int(off) >= len(b) {
				return nil, nil
			}
			end := int(off + size)
			if end > len(b) {
				end = len(b)
			}
			return b[off:end], nil
		}
	}
	return nil, nil
}

func readPEZString(f *pe.File, va uint32) (string, error) {
	b, err := readPEVA(f, va, 256)
	if err != nil || len(b) == 0 {
		return "", err
	}
	for i, c := range b {
		if c == 0 {
			return string(b[:i]), nil
		}
	}
	return string(b), nil
}

func le32(b []byte) uint32 {
	if len(b) < 4 {
		return 0
	}
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

func stripSymVer(name string) string {
	if i := strings.Index(name, "@"); i >= 0 {
		return name[:i]
	}
	return name
}

func cName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	// Mach-O C symbols are prefixed with _.
	if strings.HasPrefix(name, "_") && !strings.HasPrefix(name, "__") {
		name = name[1:]
	}
	// C++ mangled names: leave unresolved (prefer FN).
	if strings.HasPrefix(name, "_Z") || strings.HasPrefix(name, "?") {
		return ""
	}
	if !isIdentString(name) {
		return ""
	}
	return name
}

func reservedExport(name string) bool {
	switch name {
	case "main", "_start", "_init", "_fini", "__bss_start", "_end", "_edata",
		"_etext", "end", "edata", "etext", "__gmon_start__", "_Jv_RegisterClasses",
		"DllMain", "DllMainCRTStartup", "_DllMainCRTStartup":
		return true
	}
	if strings.HasPrefix(name, "__cxa_") || strings.HasPrefix(name, "__gcc_") ||
		strings.HasPrefix(name, "_mh_") || strings.HasPrefix(name, "__mh_") ||
		strings.HasPrefix(name, "__dso_") || strings.HasPrefix(name, "_ITM_") {
		return true
	}
	return false
}

func looksLikeSharedLibProject(root string, byAbs map[string]*unit) bool {
	for _, rel := range []string{
		"CMakeLists.txt", "Makefile", "makefile", "meson.build",
		"lib/CMakeLists.txt", "src/CMakeLists.txt",
		"lib/Makefile.am", "lib/Makefile",
	} {
		if cmakeLooksShared(filepath.Join(root, rel)) {
			return true
		}
	}
	hasInc, hasLibC := false, false
	for _, u := range byAbs {
		rel := filepath.ToSlash(u.File.Rel)
		if strings.HasPrefix(rel, "include/") && u.IsHeader {
			hasInc = true
		}
		if strings.HasPrefix(rel, "lib/") && !u.IsHeader {
			hasLibC = true
		}
	}
	return hasInc && hasLibC
}

func cmakeLooksShared(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	s := string(b)
	return strings.Contains(s, "-shared") ||
		strings.Contains(s, " SHARED") ||
		strings.Contains(s, " MODULE") ||
		(strings.Contains(s, "add_library") && strings.Contains(s, "SHARED"))
}
