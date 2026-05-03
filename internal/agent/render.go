package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const maxExactFloatInteger = 1 << 53

// RenderConfigYAML renders a Config Server config snapshot into the native
// service config payload that the Agent will write into the target ConfigMap.
func RenderConfigYAML(snapshot *ConfigSnapshot) ([]byte, error) {
	if snapshot == nil {
		return nil, errors.New("config snapshot is required")
	}
	root, err := yamlNode(snapshot.Config)
	if err != nil {
		return nil, fmt.Errorf("render config yaml: %w", err)
	}
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)
	if err := encoder.Encode(root); err != nil {
		_ = encoder.Close()
		return nil, fmt.Errorf("render config yaml: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("render config yaml: %w", err)
	}
	return buf.Bytes(), nil
}

// RenderEnvSH renders a resolved env_vars snapshot as shell exports for the
// target Secret's env.sh payload.
func RenderEnvSH(snapshot *EnvVarsSnapshot) ([]byte, error) {
	if snapshot == nil {
		return nil, errors.New("env vars snapshot is required")
	}
	if len(snapshot.EnvVars.SecretRefs) > 0 {
		return nil, errors.New("env vars snapshot contains unresolved secret_refs; fetch with resolve_secrets=true")
	}

	values := make(map[string]string, len(snapshot.EnvVars.Plain)+len(snapshot.EnvVars.Secrets))
	if err := addEnvValues(values, snapshot.EnvVars.Plain, "plain"); err != nil {
		return nil, err
	}
	if err := addEnvValues(values, snapshot.EnvVars.Secrets, "secrets"); err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	for _, key := range keys {
		buf.WriteString("export ")
		buf.WriteString(key)
		buf.WriteByte('=')
		buf.WriteString(shellSingleQuote(values[key]))
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

func addEnvValues(dst map[string]string, src map[string]string, source string) error {
	for key, value := range src {
		if !isValidEnvName(key) {
			return fmt.Errorf("%s env var name %q is invalid", source, key)
		}
		if strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("%s env var %q contains NUL byte", source, key)
		}
		if _, exists := dst[key]; exists {
			return fmt.Errorf("env var %q is defined more than once", key)
		}
		dst[key] = value
	}
	return nil
}

func isValidEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if r > 127 {
			return false
		}
		if i == 0 {
			if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') {
				return false
			}
			continue
		}
		if r != '_' && (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func yamlNode(value any) (*yaml.Node, error) {
	if value == nil {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil
	}
	return yamlNodeValue(reflect.ValueOf(value))
}

func yamlNodeValue(value reflect.Value) (*yaml.Node, error) {
	if !value.IsValid() {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null"}, nil
	}
	if value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null"}, nil
		}
		return yamlNodeValue(value.Elem())
	}
	if number, ok := value.Interface().(json.Number); ok {
		return yamlNumberNode(number)
	}

	switch value.Kind() {
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String {
			return nil, fmt.Errorf("unsupported map key type %s", value.Type().Key())
		}
		keys := make([]string, 0, value.Len())
		iter := value.MapRange()
		for iter.Next() {
			keys = append(keys, iter.Key().String())
		}
		sort.Strings(keys)
		node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, key := range keys {
			mapKey := reflect.New(value.Type().Key()).Elem()
			mapKey.SetString(key)
			child, err := yamlNodeValue(value.MapIndex(mapKey))
			if err != nil {
				return nil, err
			}
			node.Content = append(node.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
				child,
			)
		}
		return node, nil
	case reflect.Slice, reflect.Array:
		node := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for i := 0; i < value.Len(); i++ {
			child, err := yamlNodeValue(value.Index(i))
			if err != nil {
				return nil, err
			}
			node.Content = append(node.Content, child)
		}
		return node, nil
	case reflect.String:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value.String()}, nil
	case reflect.Bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(value.Bool())}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatInt(value.Int(), 10)}, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatUint(value.Uint(), 10)}, nil
	case reflect.Float32, reflect.Float64:
		f := value.Float()
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, fmt.Errorf("unsupported float value %v", f)
		}
		if math.Trunc(f) == f && math.Abs(f) <= maxExactFloatInteger {
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatFloat(f, 'f', 0, value.Type().Bits())}, nil
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!float", Value: strconv.FormatFloat(f, 'f', -1, value.Type().Bits())}, nil
	default:
		return nil, fmt.Errorf("unsupported config value type %s", value.Type())
	}
}

func yamlNumberNode(number json.Number) (*yaml.Node, error) {
	text := number.String()
	if !isJSONNumberLiteral(text) {
		return nil, fmt.Errorf("unsupported numeric value %q", text)
	}
	if !strings.ContainsAny(text, ".eE") {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: text}, nil
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!float", Value: text}, nil
}

func isJSONNumberLiteral(text string) bool {
	if text == "" {
		return false
	}
	i := 0
	if text[i] == '-' {
		i++
		if i == len(text) {
			return false
		}
	}
	if text[i] == '0' {
		i++
	} else {
		if text[i] < '1' || text[i] > '9' {
			return false
		}
		for i < len(text) && text[i] >= '0' && text[i] <= '9' {
			i++
		}
	}
	if i < len(text) && text[i] == '.' {
		i++
		if i == len(text) || text[i] < '0' || text[i] > '9' {
			return false
		}
		for i < len(text) && text[i] >= '0' && text[i] <= '9' {
			i++
		}
	}
	if i < len(text) && (text[i] == 'e' || text[i] == 'E') {
		i++
		if i < len(text) && (text[i] == '+' || text[i] == '-') {
			i++
		}
		if i == len(text) || text[i] < '0' || text[i] > '9' {
			return false
		}
		for i < len(text) && text[i] >= '0' && text[i] <= '9' {
			i++
		}
	}
	return i == len(text)
}
