package convert

import "testing"

func TestWrapperExitCode(t *testing.T) {
	for text, want := range map[string]int64{
		"Exit code: 2\nWall time: 0.1 seconds\nOutput:\nboom":                                         2,
		"Exit code: 0\nWall time: 1 seconds\nTotal output lines: 3\nOutput:\na":                       0,
		"Chunk ID: ab12\nWall time: 0.5000 seconds\nProcess exited with code 1\nOutput:\nno":          1,
		"Wall time: 0.5000 seconds\nProcess exited with code 127\nOriginal token count: 3\nOutput:\n": 127,
		`{"output":"x","metadata":{"exit_code":3,"duration_seconds":0.2}}`:                            3,
	} {
		if got, ok := wrapperExitCode(text); !ok || got != want {
			t.Errorf("wrapperExitCode(%q) = %d, %v; want %d", text, got, ok, want)
		}
	}
	for _, text := range []string{
		"",
		"ok",
		"Exit code: 2",
		"Exit code: x\nWall time: 1 seconds",
		"Wall time: 0.5 seconds\naborted by user",
		"Wall time: 0.5 seconds\nProcess running with session ID 4\nOutput:\n",
		"the log says Process exited with code 1\nOutput:\n",
		`{"output":"x"}`,
		`{"metadata":{"exit_code":1}}`,
	} {
		if n, ok := wrapperExitCode(text); ok {
			t.Errorf("wrapperExitCode(%q) = %d, true; want none", text, n)
		}
	}
}
