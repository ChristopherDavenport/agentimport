package claudecode

import "github.com/ChristopherDavenport/agentimport/internal/jsonline"

// Members is embedded in every object this package decodes. It keeps
// the members the Go type does not declare, and remembers which
// declared members the input carried and which were null, so an object
// decoded and encoded again is the same JSON value.
type Members = jsonline.Members

func decode(data []byte, v any, m *Members) error { return jsonline.Decode(data, v, m) }

func encode(v any, m *Members) ([]byte, error) { return jsonline.Encode(v, m) }
