package jobapp

import (
	"reflect"
	"regexp"
	"sort"
)

var taskIDPattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,127}$`)

type taskEntry struct {
	id      TaskID
	handler Task
}

type TaskRegistry struct {
	entries map[TaskID]taskEntry
}

func NewTaskRegistry(tasks ...Task) (TaskRegistry, error) {
	entries := make(map[TaskID]taskEntry, len(tasks))
	for _, handler := range tasks {
		if nilLike(handler) {
			return TaskRegistry{}, jobError("task-registry.new", CodeInvalidInput)
		}
		id := handler.ID()
		if !taskIDPattern.MatchString(string(id)) {
			return TaskRegistry{}, jobError("task-registry.new", CodeInvalidInput)
		}
		if _, exists := entries[id]; exists {
			return TaskRegistry{}, jobError("task-registry.new", CodeTaskDuplicate)
		}
		entries[id] = taskEntry{id: id, handler: handler}
	}
	return TaskRegistry{entries: entries}, nil
}

func (r TaskRegistry) IDs() []TaskID {
	result := make([]TaskID, 0, len(r.entries))
	for id := range r.entries {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func (r TaskRegistry) lookup(id TaskID) (Task, bool) {
	entry, exists := r.entries[id]
	return entry.handler, exists
}

func (r TaskRegistry) valid() bool { return r.entries != nil }

func nilLike(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
