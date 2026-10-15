package convert

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ChristopherDavenport/agentimport"
	"github.com/ChristopherDavenport/agentimport/atif"
	"github.com/ChristopherDavenport/openresponses"
)

// MaxImageSize bounds an image file read to inline. A larger one is
// not read.
const MaxImageSize = 20 << 20

// inputParts converts content to the parts of a user or system message
// or a tool output: text to input_text, an image to input_image, audio
// and other parts to extension parts carrying them.
func (c *docConv) inputParts(i int, m atif.Content) openresponses.Contents {
	if m.Parts == nil {
		if m.Raw != nil {
			return openresponses.Contents{}
		}
		return openresponses.Contents{&openresponses.InputText{Text: m.Text}}
	}
	out := openresponses.Contents{}
	for j, p := range m.Parts {
		switch {
		case p == nil || p.Err != nil:
			c.addf(agentimport.Dropped, "atif/part-malformed", i+1, "content part %d is not an object and is left out", j)
		case p.Type == atif.PartText:
			out = append(out, &openresponses.InputText{Text: p.Text})
		case p.Type == atif.PartImage:
			out = append(out, c.image(i, p))
		default:
			out = append(out, c.extensionPart(i, p))
		}
	}
	return out
}

// outputParts converts an agent message's content to output parts:
// text to output_text, media to extension parts carrying it, which an
// assistant message has no core part for.
func (c *docConv) outputParts(i int, m atif.Content) openresponses.Contents {
	if m.Parts == nil {
		return openresponses.Contents{&openresponses.OutputText{Text: m.Text}}
	}
	out := openresponses.Contents{}
	for j, p := range m.Parts {
		switch {
		case p == nil || p.Err != nil:
			c.addf(agentimport.Dropped, "atif/part-malformed", i+1, "content part %d is not an object and is left out", j)
		case p.Type == atif.PartText:
			out = append(out, &openresponses.OutputText{Text: p.Text})
		default:
			out = append(out, c.extensionPart(i, p))
		}
	}
	return out
}

// extensionPart carries a part with no Open Responses part of its own
// verbatim, typed atif.<type>: audio, which Responses has no input part
// for, an image in an agent's message, or a type ATIF does not define.
func (c *docConv) extensionPart(i int, p *atif.Part) openresponses.Content {
	typ := Source + "." + p.Type
	raw, err := json.Marshal(p)
	var obj map[string]json.RawMessage
	if err == nil && json.Unmarshal(raw, &obj) == nil {
		obj["type"], _ = json.Marshal(typ)
		raw, err = json.Marshal(obj)
	}
	if err == nil {
		raw, err = c.clean(i+1, raw)
	}
	if err != nil {
		c.addf(agentimport.Dropped, "atif/part-malformed", i+1, "a %s part cannot be carried and is left out", p.Type)
		return &openresponses.InputText{Text: ""}
	}
	switch p.Type {
	case atif.PartAudio:
		c.addf(agentimport.Kept, "atif/audio", i+1, "an audio part, which Open Responses has no input part for, is an %s part carrying it, its file not read", typ)
	case atif.PartImage:
		c.addf(agentimport.Kept, "atif/agent-media", i+1, "an image in an agent's message, which an assistant message has no part for, is an %s part carrying it", typ)
	default:
		c.addf(agentimport.Kept, "atif/part-type", i+1, "a part of type %q, which ATIF does not define, is an %s part carrying it", p.Type, typ)
	}
	return &openresponses.UnknownContent{Type: typ, Raw: raw}
}

// image converts an image part to input_image: a URL as it is, a file
// read and inlined as a data URL, as the session format's converters
// keep images. A file that cannot be read is a text placeholder naming
// it.
func (c *docConv) image(i int, p *atif.Part) openresponses.Content {
	line := i + 1
	var path, mediaType string
	if p.Source != nil && p.Source.Err == nil {
		path, mediaType = p.Source.Path, p.Source.MediaType
	}
	if path == "" {
		c.addf(agentimport.Lossy, "atif/media-missing", line, "an image part names no file; it is a text placeholder")
		return &openresponses.InputText{Text: "[image]"}
	}
	lower := strings.ToLower(path)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "data:") {
		return &openresponses.InputImage{ImageURL: path}
	}
	if strings.Contains(path, "://") {
		c.addf(agentimport.Lossy, "atif/media-missing", line, "image %s is at a URL no request can name; it is a text placeholder", path)
		return &openresponses.InputText{Text: "[image: " + path + "]"}
	}
	data, err := c.readMedia(path)
	if err != nil {
		code, kind := "atif/media-missing", agentimport.Lossy
		if errors.Is(err, errOutside) || errors.Is(err, errNoDir) {
			code, kind = "atif/media-unread", agentimport.Dropped
		}
		c.addf(kind, code, line, "image file %s is not read (%s); it is a text placeholder", path, agentimport.ErrorText(err))
		return &openresponses.InputText{Text: "[image: " + path + "]"}
	}
	if mediaType == "" {
		mediaType = imageType(path)
	}
	return &openresponses.InputImage{ImageURL: "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(data)}
}

// readMedia reads a media file a document names, inside the
// conversion's directory and no larger than MaxImageSize.
func (c *docConv) readMedia(path string) ([]byte, error) {
	real, err := c.fam.resolve(c.in.dir, path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(real)
	if err != nil {
		return nil, errors.New("no such file, or it cannot be opened")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxImageSize+1))
	if err != nil {
		return nil, errors.New("it cannot be read")
	}
	if len(data) > MaxImageSize {
		return nil, errors.New("it is larger than the converter inlines")
	}
	return data, nil
}

// imageType guesses an image's media type from its extension.
func imageType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	}
	return "image/png"
}
