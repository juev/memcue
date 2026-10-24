//go:build research

// Command report prints what an ELF binary is made of, for the size note of #325: the file size, the size of every
// section that sits in the file, and, when the binary keeps its symbol table (a build without -s), the bytes of the
// text and data symbols per package, grouped the way the note reads them.
//
// usage: go run -tags research ./docs/research/scripts/binsize/report BINARY [BINARY...]
package main

import (
	"cmp"
	"debug/elf"
	"fmt"
	"os"
	"slices"
	"strings"
)

func main() {
	for _, path := range os.Args[1:] {
		if err := report(path); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

func report(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	f, err := elf.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	fmt.Printf("== %s: %d B\n", path, st.Size())
	for _, s := range f.Sections {
		if s.Type == elf.SHT_NOBITS || s.Size == 0 {
			continue
		}
		fmt.Printf("  %-20s %10d  %5.1f%%\n", s.Name, s.Size, 100*float64(s.Size)/float64(st.Size()))
	}
	syms, err := f.Symbols()
	if err != nil {
		return nil // stripped: sections only
	}
	type pkg struct {
		name  string
		bytes uint64
		n     int
	}
	by := map[string]*pkg{}
	var total uint64
	for _, s := range syms {
		if s.Size == 0 || int(s.Section) >= len(f.Sections) || f.Sections[s.Section].Type == elf.SHT_NOBITS {
			continue
		}
		name := group(s.Name)
		p := by[name]
		if p == nil {
			p = &pkg{name: name}
			by[name] = p
		}
		p.bytes += s.Size
		p.n++
		total += s.Size
	}
	list := make([]*pkg, 0, len(by))
	for _, p := range by {
		list = append(list, p)
	}
	slices.SortFunc(list, func(a, b *pkg) int { return cmp.Compare(b.bytes, a.bytes) })
	fmt.Printf("  symbols in the file: %d B\n", total)
	for _, p := range list[:min(len(list), 25)] {
		fmt.Printf("  %-36s %10d  %5.1f%%  %6d symbols\n", p.name, p.bytes, 100*float64(p.bytes)/float64(total), p.n)
	}
	return nil
}

// group names the package family of a Go symbol: the module for a third-party path (modernc.org/sqlite), memcue's
// own packages one by one, the first element for the standard library (crypto, net), and the runtime's own kinds
// (type:, go:) as they are.
func group(sym string) string {
	for _, p := range []string{"type:", "go:", "runtime."} {
		if strings.HasPrefix(sym, p) {
			return strings.TrimSuffix(p, ".")
		}
	}
	name := sym
	if i := strings.LastIndex(name, "/"); i >= 0 {
		if j := strings.Index(name[i:], "."); j >= 0 {
			name = name[:i+j]
		}
	} else if j := strings.Index(name, "."); j >= 0 {
		name = name[:j]
	}
	parts := strings.Split(name, "/")
	switch {
	case strings.HasPrefix(name, "github.com/juev/memcue/"):
		return strings.Join(parts[:min(len(parts), 5)], "/")
	case strings.Contains(parts[0], "."):
		return strings.Join(parts[:min(len(parts), 2)], "/")
	case parts[0] == "vendor":
		return strings.Join(parts[:min(len(parts), 3)], "/")
	}
	return parts[0]
}
