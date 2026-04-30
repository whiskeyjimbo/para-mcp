package localvault

import (
	"bytes"
	"strings"

	"github.com/whiskeyjimbo/paras/internal/core/domain"
	"gopkg.in/yaml.v3"
)

const fmDelimiter = "---"

func parseNote(content []byte) (domain.FrontMatter, string, error) {
	s := string(content)

	rest, found := strings.CutPrefix(s, fmDelimiter+"\n")
	if !found {
		rest, found = strings.CutPrefix(s, fmDelimiter+"\r\n")
	}
	if !found {
		return domain.FrontMatter{}, s, nil
	}

	idx := strings.Index(rest, "\n"+fmDelimiter)
	if idx < 0 {
		return domain.FrontMatter{}, s, nil
	}
	yamlPart := rest[:idx]
	body := rest[idx+1+len(fmDelimiter):]
	body = strings.TrimLeft(body, "\r\n")

	var fm domain.FrontMatter
	if err := yaml.Unmarshal([]byte(yamlPart), &fm); err != nil {
		return domain.FrontMatter{}, s, err
	}
	return fm, body, nil
}

func formatNote(fm domain.FrontMatter, body string) ([]byte, error) {
	yamlBytes, err := yaml.Marshal(fm)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteString(fmDelimiter + "\n")
	buf.Write(yamlBytes)
	buf.WriteString(fmDelimiter + "\n")
	if body != "" {
		buf.WriteString("\n")
		buf.WriteString(body)
	}
	return buf.Bytes(), nil
}
