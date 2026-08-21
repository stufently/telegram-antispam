package config

import (
	"bytes"

	"gopkg.in/yaml.v3"
)

// UnknownKeys reports config keys the running binary does not understand.
//
// It exists because yaml.v3 silently drops unknown fields: a key that this
// version has no field for — a typo, or a setting added to values ahead of the
// image that consumes it — parses cleanly and simply never takes effect. That
// failure has no symptom at all, which is worse than a crash: the operator
// sees a healthy pod doing the old thing.
//
// It deliberately WARNS rather than refusing to start. Strict parsing at
// startup would turn a rollback (older binary, newer config) into a crash
// loop, trading a silent no-op for an outage. Callers log the result; CI can
// fail the deploy on it, where refusing is cheap.
func UnknownKeys(b []byte) error {
	var probe Config
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&probe); err != nil {
		return err
	}
	return nil
}
