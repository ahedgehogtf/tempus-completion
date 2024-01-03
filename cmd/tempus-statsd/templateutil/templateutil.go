package templateutil

import (
	"embed"
	"fmt"
	"html/template"
	"path"
	"time"
)

type TemplateGroup struct {
	Files []string
	Add   func(t *template.Template)
}

func roundDuration(d time.Duration) time.Duration {
	return time.Duration((d + 500*time.Millisecond) / time.Second * time.Second)
}

func ParseFS(fs embed.FS, groups []TemplateGroup) error {
	funcMap := template.FuncMap{
		"roundDuration": roundDuration,
	}

	for _, group := range groups {
		name := path.Base(group.Files[0])
		t := template.New(name).Funcs(funcMap)

		t, err := t.ParseFS(fs, group.Files...)
		if err != nil {
			return fmt.Errorf("parse index template: %w", err)
		}

		group.Add(t)
	}

	return nil
}
