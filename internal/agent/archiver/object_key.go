package archiver

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxObjectKeyBytes = 1024

func normalizeObjectPrefix(prefix string) (string, error) {
	prefix = strings.TrimSpace(prefix)
	if unescaped, err := url.PathUnescape(prefix); err == nil {
		prefix = unescaped
	} else {
		return "", fmt.Errorf("object prefix contains invalid escaping: %w", err)
	}
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return "", nil
	}
	return validateObjectKey("object prefix", prefix)
}

func joinObjectKey(prefix string, key string) (string, error) {
	key, err := validateObjectKey("object key", key)
	if err != nil {
		return "", err
	}
	if prefix == "" {
		return key, nil
	}

	prefix, err = normalizeObjectPrefix(prefix)
	if err != nil {
		return "", err
	}
	if prefix == "" {
		return key, nil
	}
	return prefix + "/" + key, nil
}

func validateObjectKey(field string, key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	if len(key) > maxObjectKeyBytes {
		return "", fmt.Errorf("%s exceeds %d bytes", field, maxObjectKeyBytes)
	}
	if !utf8.ValidString(key) {
		return "", fmt.Errorf("%s is not valid UTF-8", field)
	}
	if strings.HasPrefix(key, "/") || strings.HasPrefix(key, `\`) {
		return "", fmt.Errorf("%s must be relative", field)
	}
	if strings.Contains(key, `\`) {
		return "", fmt.Errorf("%s must use forward slashes", field)
	}
	if strings.Contains(key, "://") {
		return "", fmt.Errorf("%s must not contain a URL scheme", field)
	}
	if strings.ContainsFunc(key, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return "", fmt.Errorf("%s contains control characters", field)
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("%s contains an unsafe path segment", field)
		}
	}
	return key, nil
}

func validateArchiveComponent(field string, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", field)
	}
	if len(value) > 255 {
		return "", fmt.Errorf("%s exceeds 255 bytes", field)
	}
	if !utf8.ValidString(value) {
		return "", fmt.Errorf("%s is not valid UTF-8", field)
	}
	if strings.Contains(value, "/") || strings.Contains(value, `\`) || strings.Contains(value, "://") {
		return "", fmt.Errorf("%s contains unsafe path or URL characters", field)
	}
	if strings.ContainsFunc(value, func(r rune) bool {
		return r < 0x20 || r == 0x7f || unicode.IsSpace(r)
	}) {
		return "", fmt.Errorf("%s contains control or whitespace characters", field)
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return "", fmt.Errorf("%s contains unsupported character %q", field, r)
	}
	return value, nil
}
