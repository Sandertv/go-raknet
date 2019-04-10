package raknet

const (
	version = "version"
)

// Protocol returns a version setting using the protocol version passed. The setting may be passed into a
// raknet.Dial() or raknet.Listen() call.
func Protocol(protocolVersion byte) Setting {
	return Setting{key: version, value: protocolVersion}
}

// Setting is a container of a setting with a key and a value.
type Setting struct {
	key   string
	value interface{}
}

// mapSettings maps a slice of settings passed to a map indexed by the keys of the settings, with the values
// as the settings' values.
func mapSettings(settings []Setting) map[string]interface{} {
	m := make(map[string]interface{}, len(settings))
	for _, setting := range settings {
		m[setting.key] = setting.value
	}
	return m
}
