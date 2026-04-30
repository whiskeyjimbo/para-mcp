package localvault

import (
	"slices"
	"strings"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"gopkg.in/yaml.v3"
)

// canonicalFrontMatterYAML serializes fm to YAML with the "derived" key
// excluded and all keys sorted, giving a stable input for ETag hashing.
func canonicalFrontMatterYAML(fm domain.FrontMatter) string {
	var doc yaml.Node
	if err := doc.Encode(fm); err != nil {
		panic("canonicalFrontMatterYAML: " + err.Error())
	}
	mapping := &doc
	if doc.Kind == yaml.DocumentNode {
		if len(doc.Content) == 0 {
			return ""
		}
		mapping = doc.Content[0]
	}
	removeMappingKey(mapping, "derived")
	sortMappingKeys(mapping)

	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	_ = enc.Encode(mapping)
	_ = enc.Close()
	return b.String()
}

func removeMappingKey(mapping *yaml.Node, key string) {
	out := mapping.Content[:0]
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			continue
		}
		out = append(out, mapping.Content[i], mapping.Content[i+1])
	}
	mapping.Content = out
}

func sortMappingKeys(mapping *yaml.Node) {
	type pair struct{ k, v *yaml.Node }
	pairs := make([]pair, 0, len(mapping.Content)/2)
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		pairs = append(pairs, pair{mapping.Content[i], mapping.Content[i+1]})
	}
	slices.SortFunc(pairs, func(a, b pair) int {
		return strings.Compare(a.k.Value, b.k.Value)
	})
	for i, p := range pairs {
		mapping.Content[2*i] = p.k
		mapping.Content[2*i+1] = p.v
	}
}
