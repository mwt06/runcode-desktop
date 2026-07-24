package command

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gitlab.ouc-online.com.cn/aibase/agentloop/toolpath"
)

const (
	// DefaultMaxBodyBytes bounds a single command body so a runaway file cannot
	// dominate a prompt.
	DefaultMaxBodyBytes = 64 * 1024
	// DefaultMaxCommands bounds how many commands load across all roots.
	DefaultMaxCommands = 200
	// fileExt is the command file extension.
	fileExt = ".md"
)

// Root is a directory to scan for command files, tagged with a source.
type Root struct {
	Dir    string
	Source Source
}

// LoadOptions controls command discovery.
type LoadOptions struct {
	Roots        []Root
	MaxBodyBytes int
	MaxCommands  int
}

// Problem records a command file that could not be loaded, with a sanitized
// reason naming the path rather than echoing contents.
type Problem struct {
	Path   string
	Reason string
}

func (p Problem) String() string { return fmt.Sprintf("%s: %s", p.Path, p.Reason) }

// Load discovers commands under each root: every *.md file becomes a command
// named after the file (without the extension). Loading is tolerant — a bad file
// is skipped with a Problem rather than failing the whole load; a missing root is
// not a problem.
func Load(opts LoadOptions) (*Set, []Problem) {
	maxBody := opts.MaxBodyBytes
	if maxBody <= 0 {
		maxBody = DefaultMaxBodyBytes
	}
	maxCommands := opts.MaxCommands
	if maxCommands <= 0 {
		maxCommands = DefaultMaxCommands
	}

	var (
		commands []Command
		problems []Problem
		capped   bool
	)
	for _, root := range opts.Roots {
		dir, err := normalizeDir(root.Dir)
		if err != nil {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			if !os.IsNotExist(err) {
				problems = append(problems, Problem{Path: dir, Reason: "cannot read commands directory"})
			}
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), fileExt) {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			cmd, problem := loadCommand(dir, path, entry.Name(), root.Source, maxBody)
			if problem != nil {
				problems = append(problems, *problem)
				continue
			}
			if len(commands) >= maxCommands {
				capped = true
				continue
			}
			commands = append(commands, cmd)
		}
	}
	if capped {
		problems = append(problems, Problem{Path: "", Reason: fmt.Sprintf("more than %d commands found; extra commands were skipped", maxCommands)})
	}
	return NewSet(commands), problems
}

func loadCommand(root, path, fileName string, source Source, maxBody int) (Command, *Problem) {
	name := strings.TrimSuffix(fileName, fileExt)
	if !validName(name) {
		return Command{}, &Problem{Path: path, Reason: "invalid command name (use letters, digits, '-' or '_')"}
	}
	info, err := os.Lstat(path)
	if err != nil {
		return Command{}, &Problem{Path: path, Reason: "cannot stat command file"}
	}
	if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
		return Command{}, &Problem{Path: path, Reason: "command file is not a regular file"}
	}
	within, err := toolpath.IsWithinResolved(root, path)
	if err != nil || !within {
		return Command{}, &Problem{Path: path, Reason: "command file resolves outside the commands directory"}
	}

	file, err := os.Open(path)
	if err != nil {
		return Command{}, &Problem{Path: path, Reason: "cannot open command file"}
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, int64(maxBody)+1))
	if err != nil {
		return Command{}, &Problem{Path: path, Reason: "cannot read command file"}
	}
	truncated := len(data) > maxBody
	if truncated {
		data = truncateUTF8(data, maxBody)
	}

	p := parseCommand(string(data))
	if strings.TrimSpace(p.Body) == "" {
		return Command{}, &Problem{Path: path, Reason: "command body is empty"}
	}
	description := p.Description
	if description == "" {
		description = "custom command"
	}
	return Command{
		Name:         name,
		Description:  description,
		ArgumentHint: p.ArgumentHint,
		Body:         p.Body,
		Path:         path,
		Source:       source,
		Truncated:    truncated,
	}, nil
}

// truncateUTF8 trims data to at most limit bytes without splitting a multi-byte
// rune at the boundary. The parameter avoids the built-in name max.
func truncateUTF8(data []byte, limit int) []byte {
	if len(data) <= limit {
		return data
	}
	end := limit
	for end > 0 && data[end]&0xC0 == 0x80 { // mid-rune continuation byte
		end--
	}
	return data[:end]
}

func normalizeDir(dir string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("empty directory")
	}
	abs, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return "", err
	}
	return abs, nil
}
