package domain

// CategoryTemplate defines default frontmatter applied on note creation.
type CategoryTemplate struct {
	Status string
	Tags   []string
}

// DefaultTemplates are the built-in per-category creation defaults.
var DefaultTemplates = map[Category]CategoryTemplate{
	Projects:  {Status: "active"},
	Areas:     {},
	Resources: {},
	Archives:  {Status: "archived"},
}

// frontMatterSetters maps known field names to their setter functions.
// Adding a new frontmatter field is an additive change: register it here.
var frontMatterSetters = map[string]func(*FrontMatter, any){
	"title": func(fm *FrontMatter, v any) {
		if s, ok := v.(string); ok {
			fm.Title = s
		}
	},
	"status": func(fm *FrontMatter, v any) {
		if s, ok := v.(string); ok {
			fm.Status = NormalizeStatus(s)
		}
	},
	"area": func(fm *FrontMatter, v any) {
		if s, ok := v.(string); ok {
			fm.Area = s
		}
	},
	"project": func(fm *FrontMatter, v any) {
		if s, ok := v.(string); ok {
			fm.Project = s
		}
	},
	"tags": func(fm *FrontMatter, v any) {
		switch tv := v.(type) {
		case []string:
			fm.Tags = NormalizeTags(tv)
		case []any:
			tags := make([]string, 0, len(tv))
			for _, t := range tv {
				if s, ok := t.(string); ok {
					tags = append(tags, s)
				}
			}
			fm.Tags = NormalizeTags(tags)
		}
	},
}

// ApplyFrontMatterPatch applies a partial update to fm from a fields map.
func ApplyFrontMatterPatch(fm *FrontMatter, fields map[string]any) {
	for k, v := range fields {
		if setter, ok := frontMatterSetters[k]; ok {
			setter(fm, v)
		} else {
			if fm.Extra == nil {
				fm.Extra = make(map[string]any)
			}
			fm.Extra[k] = v
		}
	}
}
