package pi

import "github.com/ChristopherDavenport/agentimport/internal/jsonline"

// Members is embedded in every object this package decodes. It keeps
// the members the Go type does not declare, and remembers which
// declared members the input carried and which were null, so an object
// decoded and encoded again is the same JSON value.
type Members = jsonline.Members

func encode(v any, m *Members) ([]byte, error) { return jsonline.Encode(v, m) }

// decodeLenient decodes every pi object: each is a union of shapes, so
// a member of another JSON type is kept as written rather than failing
// the object.
func decodeLenient(data []byte, v any, m *Members) error {
	return jsonline.DecodeLenient(data, v, m)
}
