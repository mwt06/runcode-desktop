package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/wt68/runcode/engine/llm"
	"github.com/wt68/runcode/engine/tool"
	"github.com/wt68/runcode/engine/toolpath"
)

const (
	// maxAttachmentBytes bounds a single inlined image.
	maxAttachmentBytes = 5 << 20 // 5 MiB
	// maxAttachments bounds how many images one message may carry.
	maxAttachments = 8
)

// imageMediaTypes maps recognized image extensions to their media type.
var imageMediaTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
}

// imageRefPattern matches an @-prefixed file reference token (path runs to the
// next whitespace).
var imageRefPattern = regexp.MustCompile(`@(\S+)`)

// horizontalGap collapses runs of spaces/tabs left after stripping a reference.
var horizontalGap = regexp.MustCompile(`[ \t]{2,}`)

// parseImageAttachments extracts @path image references from input. Each ref that
// resolves to an existing image file inside the workspace is loaded as an image
// source and removed from the returned text; references that are not workspace
// images are left in the text untouched. cwd is the workspace root used to
// resolve relative paths safely (out-of-workspace paths are ignored).
func parseImageAttachments(input, cwd string) (string, []llm.ImageSource) {
	if !strings.Contains(input, "@") || cwd == "" {
		return input, nil
	}
	tctx := &tool.Context{WorkingDirectory: cwd}
	workspace, err := toolpath.WorkspaceRoot(tctx)
	if err != nil {
		return input, nil
	}

	var images []llm.ImageSource
	text := imageRefPattern.ReplaceAllStringFunc(input, func(match string) string {
		if len(images) >= maxAttachments {
			return match
		}
		ref := strings.TrimPrefix(match, "@")
		img, ok := loadWorkspaceImage(ref, workspace, tctx)
		if !ok {
			return match // not a workspace image: leave the token in the prose
		}
		images = append(images, img)
		return ""
	})
	if len(images) == 0 {
		return input, nil
	}
	text = strings.TrimSpace(horizontalGap.ReplaceAllString(text, " "))
	return text, images
}

// loadWorkspaceImage loads an image file referenced relative to the workspace,
// returning ok=false if it is not a recognized, in-workspace, sanely-sized image.
func loadWorkspaceImage(ref, workspace string, tctx *tool.Context) (llm.ImageSource, bool) {
	mediaType, ok := imageMediaTypes[strings.ToLower(filepath.Ext(ref))]
	if !ok {
		return llm.ImageSource{}, false
	}
	resolved, err := toolpath.Resolve(ref, tctx)
	if err != nil {
		return llm.ImageSource{}, false
	}
	within, err := toolpath.IsWithinResolved(workspace, resolved)
	if err != nil || !within {
		return llm.ImageSource{}, false
	}
	info, err := os.Stat(resolved)
	if err != nil || info.IsDir() || info.Size() == 0 || info.Size() > maxAttachmentBytes {
		return llm.ImageSource{}, false
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return llm.ImageSource{}, false
	}
	return llm.ImageSource{MediaType: mediaType, Data: data}, true
}
