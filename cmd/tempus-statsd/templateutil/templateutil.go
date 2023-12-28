package templateutil

import (
	"embed"
	"fmt"
	"html/template"
)

type TemplateGroup struct {
	Files []string
	Add   func(t *template.Template)
}

func ParseFS(fs embed.FS, groups []TemplateGroup) error {
	for _, group := range groups {
		t, err := template.ParseFS(fs, group.Files...)
		if err != nil {
			return fmt.Errorf("parse index template: %w", err)
		}

		group.Add(t)
	}

	return nil
}
