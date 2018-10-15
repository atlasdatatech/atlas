package glyphs

import (
	"sort"
	"strings"

	log "github.com/sirupsen/logrus"

	proto "github.com/golang/protobuf/proto"
)

//Combine combine glyph (SDF) PBFs to one
//Returns a re-encoded PBF with the combined
//font faces, composited using array order
//to determine glyph priority.
//@param buffers An array of SDF PBFs.
func Combine(buffers [][]byte, fontstack []string) ([]byte, error) {
	coverage := make(map[uint32]bool)
	result := &Glyphs{}
	for i, buf := range buffers {
		pbf := &Glyphs{}
		err := proto.Unmarshal(buf, pbf)
		if err != nil {
			log.Fatal("unmarshaling error: ", err)
		}

		if stacks := pbf.GetStacks(); stacks != nil && len(stacks) > 0 {
			stack := stacks[0]
			if 0 == i {
				for _, gly := range stack.Glyphs {
					coverage[gly.GetId()] = true
				}
				result = pbf
			} else {
				for _, gly := range stack.Glyphs {
					if !coverage[gly.GetId()] {
						result.Stacks[0].Glyphs = append(result.Stacks[0].Glyphs, gly)
						coverage[gly.GetId()] = true
					}
				}
				result.Stacks[0].Name = proto.String(result.Stacks[0].GetName() + "," + stack.GetName())
			}
		}

		if fontstack != nil {
			result.Stacks[0].Name = proto.String(strings.Join(fontstack, ","))
		}
	}

	glys := result.Stacks[0].GetGlyphs()

	sort.Slice(glys, func(i, j int) bool {
		return glys[i].GetId() < glys[j].GetId()
	})

	return proto.Marshal(result)
}
