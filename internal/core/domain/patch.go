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

// ApplyFrontMatterPatch applies a partial update to fm from a fields map.
func ApplyFrontMatterPatch(fm *FrontMatter, fields map[string]any) {
	for k, v := range fields {
		switch k {
		case "title":
			if s, ok := v.(string); ok {
				fm.Title = s
			}
		case "status":
			if s, ok := v.(string); ok {
				fm.Status = NormalizeStatus(s)
			}
		case "area":
			if s, ok := v.(string); ok {
				fm.Area = s
			}
		case "project":
			if s, ok := v.(string); ok {
				fm.Project = s
			}
		case "tags":
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
		default:
			if fm.Extra == nil {
				fm.Extra = make(map[string]any)
			}
			fm.Extra[k] = v
		}
	}
}
