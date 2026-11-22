package index

// computeTestCoverage derives test reachability without executing go test. It
// seeds from the type-checked call edges of indexed test functions, so build
// tags, receiver types, and package boundaries are honored exactly.
func (b *builder) computeTestCoverage() {
	seeds := b.testCoverageSeeds()
	testReferencedTypes := b.testReferencedTypes()
	covered := b.expandTestCoverage(seeds, testReferencedTypes)
	for id := range covered {
		if sym := b.idx.Symbols[id]; sym != nil && !sym.IsTest {
			b.idx.TestCovered[id] = true
		}
	}
}

func (b *builder) testCoverageSeeds() map[string]bool {
	seeds := map[string]bool{}
	for id, sym := range b.idx.Symbols {
		if !sym.IsTest || (sym.Kind != "func" && sym.Kind != "method") {
			continue
		}
		for _, callee := range b.idx.Callees[id] {
			seeds[callee] = true
		}
	}
	return seeds
}

// testReferencedTypes prevents interface dispatch from marking a production
// adapter as covered when a test only wires a mock. A dispatched concrete
// receiver is followed only when tests reference or construct that type.
func (b *builder) testReferencedTypes() map[string]bool {
	referenced := map[string]bool{}
	for id, refs := range b.idx.XRefs {
		sym := b.idx.Symbols[id]
		if sym == nil || (sym.Kind != "type" && sym.Kind != "struct" && sym.Kind != "interface") {
			continue
		}
		for _, ref := range refs {
			if ref.Test {
				referenced[id] = true
				break
			}
		}
	}
	for typeID, sites := range b.idx.CompositeLiterals {
		for _, site := range sites {
			if site.Test {
				referenced[typeID] = true
				break
			}
		}
	}
	return referenced
}

func (b *builder) expandTestCoverage(seeds, testReferencedTypes map[string]bool) map[string]bool {
	covered := make(map[string]bool, len(seeds))
	queue := make([]string, 0, len(seeds))
	for id := range seeds {
		covered[id] = true
		queue = append(queue, id)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, callee := range b.idx.Callees[cur] {
			if covered[callee] {
				continue
			}
			if edge, ok := b.idx.CallEdgeKinds[cur+"\x00"+callee]; ok && edge.Kind == "interface_dispatch" {
				target := b.idx.Symbols[callee]
				if target != nil && !target.IsTest && !testReferencedTypes[target.PkgPath+"."+target.RecvType] {
					continue
				}
			}
			covered[callee] = true
			queue = append(queue, callee)
		}
	}
	return covered
}
