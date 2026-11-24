package report

import (
	"sort"

	"github.com/yanulabs/goray/internal/index"
	"github.com/yanulabs/goray/internal/render"
)

func buildInterfaces(idx *index.Index, canonicalByExisting map[string]string) []render.HTMLInterface {
	out := make([]render.HTMLInterface, 0)
	for _, sym := range idx.Symbols {
		if sym.Kind != "interface" {
			continue
		}
		id := sym.CanonicalID()
		if id == "" {
			continue
		}
		iface := render.HTMLInterface{
			ID: id, Name: sym.Name, PkgPath: sym.PkgPath,
			Module: sym.ModuleArea, Methods: sym.Methods,
			Implementations: make([]render.HTMLImpl, 0),
		}
		for _, match := range idx.IfaceImpls[sym.ID] {
			impl := idx.Symbols[match.TypeID]
			if impl == nil {
				continue
			}
			implID := canonicalByExisting[impl.ID]
			if implID == "" {
				continue
			}
			iface.Implementations = append(iface.Implementations, render.HTMLImpl{
				ID: implID, Name: impl.Name, PkgPath: impl.PkgPath, ViaPtr: match.ViaPtr,
			})
		}
		sort.Slice(iface.Implementations, func(i, j int) bool {
			if iface.Implementations[i].PkgPath != iface.Implementations[j].PkgPath {
				return iface.Implementations[i].PkgPath < iface.Implementations[j].PkgPath
			}
			return iface.Implementations[i].Name < iface.Implementations[j].Name
		})
		out = append(out, iface)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PkgPath != out[j].PkgPath {
			return out[i].PkgPath < out[j].PkgPath
		}
		return out[i].Name < out[j].Name
	})
	return out
}
