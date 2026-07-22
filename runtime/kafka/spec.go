package kafka

import (
	"fmt"
	"reflect"
	"unicode"
	"unicode/utf8"
)

// SubscriptionSpec is the caller-owned subscription configuration.
type SubscriptionSpec struct {
	ID      string
	Group   string
	Topics  []string
	Handler Handler
}

// Subscription is an immutable validated subscription.
type Subscription struct {
	id      string
	group   string
	topics  []string
	handler Handler
	valid   bool
}

func NewSubscription(spec SubscriptionSpec) (Subscription, error) {
	if !validSubscriptionID(spec.ID) {
		return Subscription{}, configurationInvalid(errorKindSubscriptionIDInvalid, "/id")
	}
	if !validGroup(spec.Group) {
		return Subscription{}, configurationInvalid(errorKindGroupInvalid, "/group")
	}
	if len(spec.Topics) == 0 {
		return Subscription{}, configurationInvalid(errorKindTopicsEmpty, "/topics")
	}
	for index, topic := range spec.Topics {
		if !validTopic(topic) {
			return Subscription{}, configurationInvalid(errorKindTopicInvalid, fmt.Sprintf("/topics/%d", index))
		}
	}
	seen := make(map[string]struct{}, len(spec.Topics))
	for index, topic := range spec.Topics {
		if _, exists := seen[topic]; exists {
			return Subscription{}, configurationInvalid(errorKindTopicDuplicate, fmt.Sprintf("/topics/%d", index))
		}
		seen[topic] = struct{}{}
	}
	if nilHandler(spec.Handler) {
		return Subscription{}, configurationInvalid(errorKindHandlerNil, "/handler")
	}
	return Subscription{
		id:      spec.ID,
		group:   spec.Group,
		topics:  cloneStrings(spec.Topics),
		handler: spec.Handler,
		valid:   true,
	}, nil
}

func (s Subscription) ID() string { return s.id }

func (s Subscription) Group() string { return s.group }

func (s Subscription) Topics() []string { return cloneStrings(s.topics) }

func (s Subscription) Handler() Handler { return s.handler }

func (s Subscription) Valid() bool { return s.valid }

func validTopic(topic string) bool {
	return topic != "." && topic != ".." && validText(topic, 1, 249)
}

func validGroup(group string) bool {
	return validText(group, 1, 255)
}

func validHeaderKey(key string) bool {
	return validText(key, 1, 256)
}

func validText(value string, minimum, maximum int) bool {
	if len(value) < minimum || len(value) > maximum || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validSubscriptionID(id string) bool {
	if len(id) < 1 || len(id) > 128 || id[0] < 'a' || id[0] > 'z' {
		return false
	}
	previousSeparator := false
	for index := 1; index < len(id); index++ {
		character := id[index]
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') {
			previousSeparator = false
			continue
		}
		if character != '.' && character != '_' && character != '-' {
			return false
		}
		if previousSeparator || index == len(id)-1 {
			return false
		}
		previousSeparator = true
	}
	return true
}

func validateHeaders(headers []Header) error {
	for index, header := range headers {
		if !validHeaderKey(header.Key) {
			return configurationInvalid(errorKindHeaderKeyInvalid, fmt.Sprintf("/headers/%d/key", index))
		}
	}
	return nil
}

func nilHandler(handler Handler) bool {
	if handler == nil {
		return true
	}
	value := reflect.ValueOf(handler)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	cloned := make([]string, len(values))
	copy(cloned, values)
	return cloned
}

func recordPointer(index int) string {
	return fmt.Sprintf("/records/%d", index)
}
