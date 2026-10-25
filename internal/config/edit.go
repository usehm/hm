package config

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// UserTemplate starts a new user config file.
func UserTemplate() []byte {
	return []byte("# yaml-language-server: $schema=" + SchemaPath() + "\n" +
		"# hm settings. Keys you leave out keep their built-in default;\n" +
		"# run \"Settings: Show Default Config\" to see every key.\n")
}

// SetValue sets the setting at key path to value in the YAML file at path,
// creating the file and any missing parent keys. Everything else in the
// file, comments and key order included, is kept.
func SetValue(path string, key []string, value any) error {
	return editFile(path, func(root *yaml.Node) error {
		var vn yaml.Node
		if err := vn.Encode(value); err != nil {
			return err
		}
		m := root
		for i, k := range key {
			j := mappingIndex(m, k)
			last := i == len(key)-1
			if j < 0 {
				kn := &yaml.Node{Kind: yaml.ScalarNode, Value: k}
				next := &yaml.Node{Kind: yaml.MappingNode}
				if last {
					next = &vn
				}
				m.Content = append(m.Content, kn, next)
				m = next
				continue
			}
			cur := m.Content[j+1]
			if last {
				vn.LineComment, vn.HeadComment, vn.FootComment = cur.LineComment, cur.HeadComment, cur.FootComment
				if cur.Kind == vn.Kind {
					vn.Style = cur.Style
				}
				m.Content[j+1] = &vn
				return nil
			}
			if cur.Kind != yaml.MappingNode {
				// A scalar or null in the way: turn it into a mapping.
				cur = &yaml.Node{Kind: yaml.MappingNode, HeadComment: cur.HeadComment, LineComment: cur.LineComment}
				m.Content[j+1] = cur
			}
			m = cur
		}
		return nil
	})
}

// Unset removes the setting at key path, so the layer below applies again.
func Unset(path string, key []string) error {
	return editFile(path, func(root *yaml.Node) error {
		m := root
		for i, k := range key {
			j := mappingIndex(m, k)
			if j < 0 {
				return nil
			}
			if i == len(key)-1 {
				m.Content = append(m.Content[:j], m.Content[j+2:]...)
				return nil
			}
			if m = m.Content[j+1]; m.Kind != yaml.MappingNode {
				return nil
			}
		}
		return nil
	})
}

func editFile(path string, edit func(root *yaml.Node) error) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		data = UserTemplate()
	} else if err != nil {
		return err
	}
	var doc yaml.Node
	if len(bytes.TrimSpace(data)) > 0 {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return errors.New("fix the YAML errors in " + path + " first: " + err.Error())
		}
	}
	// A file with only comments parses to nothing; keep its text as a header.
	var header []byte
	if len(doc.Content) == 0 || isNull(doc.Content[0]) {
		header = bytes.TrimRight(data, "\n")
		if len(header) > 0 {
			header = append(header, '\n')
		}
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return errors.New(path + " is not a mapping of settings")
	}
	if err := edit(root); err != nil {
		return err
	}
	var out bytes.Buffer
	out.Write(header)
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
