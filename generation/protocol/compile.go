package protocol

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/bufbuild/protocompile"
	"github.com/bufbuild/protocompile/linker"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

var serviceIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

type resolverAdapter struct {
	ctx      context.Context
	owner    Resolver
	mu       sync.Mutex
	failures map[string]error
}

func (r *resolverAdapter) FindFileByPath(filePath string) (protocompile.SearchResult, error) {
	if filePath == optionsProtoPath {
		return protocompile.SearchResult{Source: bytes.NewReader(embeddedOptionsProto)}, nil
	}
	if strings.HasPrefix(filePath, "google/protobuf/") {
		return protocompile.SearchResult{}, os.ErrNotExist
	}
	if err := r.ctx.Err(); err != nil {
		return protocompile.SearchResult{}, err
	}
	reader, err := r.owner.Open(r.ctx, filePath)
	if err != nil {
		r.record(filePath, err)
		return protocompile.SearchResult{}, err
	}
	if reader == nil {
		err = errors.New("resolver returned nil reader")
		r.record(filePath, err)
		return protocompile.SearchResult{}, err
	}
	data, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil {
		r.record(filePath, readErr)
		return protocompile.SearchResult{}, readErr
	}
	if closeErr != nil {
		r.record(filePath, closeErr)
		return protocompile.SearchResult{}, closeErr
	}
	return protocompile.SearchResult{Source: bytes.NewReader(data)}, nil
}
func (r *resolverAdapter) record(filePath string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failures == nil {
		r.failures = make(map[string]error)
	}
	if _, exists := r.failures[filePath]; !exists {
		r.failures[filePath] = err
	}
}
func (r *resolverAdapter) failure() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.failures) == 0 {
		return "", nil
	}
	paths := make([]string, 0, len(r.failures))
	for filePath := range r.failures {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
	return paths[0], r.failures[paths[0]]
}

func Compile(ctx context.Context, options CompileOptions) (Document, error) {
	if ctx == nil {
		return Document{}, protocolError("protocol_input_invalid", "context_missing", "", "/context", "compile context is required")
	}
	if err := ctx.Err(); err != nil {
		return Document{}, cancellationError(err)
	}
	if !serviceIDPattern.MatchString(options.ServiceID) {
		return Document{}, protocolError("protocol_input_invalid", "service_id_invalid", "", "/serviceID", "service id is invalid")
	}
	if len(options.EntryFiles) == 0 {
		return Document{}, protocolError("protocol_input_invalid", "entry_files_missing", "", "/entryFiles", "at least one entry file is required")
	}
	if options.Resolver == nil {
		return Document{}, protocolError("protocol_input_invalid", "resolver_missing", "", "/resolver", "resolver is required")
	}
	entries := append([]string(nil), options.EntryFiles...)
	seen := map[string]struct{}{}
	for i, filePath := range entries {
		if filePath == "" || path.IsAbs(filePath) || path.Clean(filePath) != filePath || !strings.HasSuffix(filePath, ".proto") {
			return Document{}, protocolError("protocol_input_invalid", "entry_file_invalid", "", "/entryFiles", "entry file is invalid")
		}
		if _, duplicate := seen[filePath]; duplicate {
			return Document{}, protocolError("protocol_input_invalid", "entry_file_duplicate", filePath, "/entryFiles", "entry file is duplicated")
		}
		seen[filePath] = struct{}{}
		entries[i] = filePath
	}
	sort.Strings(entries)
	adapter := &resolverAdapter{ctx: ctx, owner: options.Resolver}
	compiler := protocompile.Compiler{Resolver: protocompile.WithStandardImports(adapter), SourceInfoMode: protocompile.SourceInfoStandard | protocompile.SourceInfoExtraOptionLocations}
	requested := append(append([]string(nil), entries...), optionsProtoPath)
	compiled, err := compiler.Compile(ctx, requested...)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Document{}, cancellationError(ctxErr)
		}
		if source, resolverErr := adapter.failure(); resolverErr != nil {
			return Document{}, protocolError("protocol_resolver_failed", "source_open_failed", source, "", "Proto source resolver failed")
		}
		return Document{}, protocolError("protocol_compile_failed", "descriptor_link_failed", "", "", "Proto descriptor compilation failed")
	}
	return projectDocument(options.ServiceID, entries, compiled)
}

func cancellationError(err error) *Error {
	reason := "context_cancelled"
	if errors.Is(err, context.DeadlineExceeded) {
		reason = "context_deadline_exceeded"
	}
	return protocolError("protocol_compile_cancelled", reason, "", "", "Proto descriptor compilation was cancelled")
}

func projectDocument(serviceID string, entries []string, compiled linker.Files) (Document, error) {
	optionFile := compiled.FindFileByPath(optionsProtoPath)
	if optionFile == nil {
		return Document{}, protocolError("protocol_compile_failed", "options_descriptor_missing", optionsProtoPath, "", "embedded options descriptor is missing")
	}
	httpExtension, ok := optionFile.FindDescriptorByName("nexa.protocol.v1.http_proxy").(protoreflect.ExtensionDescriptor)
	if !ok {
		return Document{}, protocolError("protocol_compile_failed", "options_descriptor_invalid", optionsProtoPath, "", "embedded HTTP proxy extension is invalid")
	}
	httpExtensionType := dynamicpb.NewExtensionType(httpExtension)
	rpcContextExtension, ok := optionFile.FindDescriptorByName("nexa.protocol.v1.rpc_context").(protoreflect.ExtensionDescriptor)
	if !ok {
		return Document{}, protocolError("protocol_compile_failed", "options_descriptor_invalid", optionsProtoPath, "", "embedded RPC context extension is invalid")
	}
	rpcContextExtensionType := dynamicpb.NewExtensionType(rpcContextExtension)
	consumerFiles := map[string]linker.File{}
	var visit func(linker.File)
	visit = func(file linker.File) {
		if file == nil || file.Path() == optionsProtoPath || strings.HasPrefix(file.Path(), "google/protobuf/") {
			return
		}
		if _, exists := consumerFiles[file.Path()]; exists {
			return
		}
		consumerFiles[file.Path()] = file
		imports := file.Imports()
		for i := 0; i < imports.Len(); i++ {
			visit(file.FindImportByPath(imports.Get(i).Path()))
		}
	}
	for _, entry := range entries {
		visit(compiled.FindFileByPath(entry))
	}
	paths := make([]string, 0, len(consumerFiles))
	for filePath := range consumerFiles {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
	state := &documentState{serviceID: serviceID, messages: map[string]*messageState{}, enums: map[string]*enumState{}, services: map[string]*serviceState{}, methods: map[string]*methodState{}}
	for _, filePath := range paths {
		file, err := projectFile(consumerFiles[filePath], httpExtensionType, rpcContextExtensionType, state)
		if err != nil {
			return Document{}, err
		}
		state.files = append(state.files, file)
	}
	operationIDs := make(map[string]struct{})
	routes := make(map[string]struct{})
	methodNames := make([]string, 0, len(state.methods))
	for name := range state.methods {
		methodNames = append(methodNames, name)
	}
	sort.Strings(methodNames)
	for _, name := range methodNames {
		method := state.methods[name]
		if method.httpProxy == nil {
			continue
		}
		if _, duplicate := operationIDs[method.httpProxy.operationID]; duplicate {
			return Document{}, protocolError("protocol_ir_invalid", "operation_id_duplicate", method.filePath, "", "HTTP proxy operation id is duplicated")
		}
		operationIDs[method.httpProxy.operationID] = struct{}{}
		route := string(method.httpProxy.method) + "\x00" + method.httpProxy.path
		if _, duplicate := routes[route]; duplicate {
			return Document{}, protocolError("protocol_ir_invalid", "route_duplicate", method.filePath, "", "HTTP proxy route is duplicated")
		}
		routes[route] = struct{}{}
	}
	if err := validateTenantContextTypes(state, methodNames); err != nil {
		return Document{}, err
	}
	if err := finalizeSources(state); err != nil {
		return Document{}, err
	}
	return Document{state: state}, nil
}

func projectFile(file linker.File, httpExtensionType, rpcContextExtensionType protoreflect.ExtensionType, document *documentState) (*fileState, error) {
	if file.Syntax() != protoreflect.Proto3 {
		return nil, protocolError("protocol_ir_invalid", "syntax_unsupported", file.Path(), "", "Protocol entry files must use proto3 syntax")
	}
	state := &fileState{path: file.Path()}
	for i := 0; i < file.Messages().Len(); i++ {
		projectMessage(file.Messages().Get(i), file, state, document)
	}
	for i := 0; i < file.Enums().Len(); i++ {
		projectEnum(file.Enums().Get(i), file, state, document)
	}
	for i := 0; i < file.Services().Len(); i++ {
		service := file.Services().Get(i)
		serviceState := &serviceState{fullName: string(service.FullName()), filePath: file.Path(), location: descriptorLocation(file, service)}
		for j := 0; j < service.Methods().Len(); j++ {
			method := service.Methods().Get(j)
			methodState := &methodState{fullName: string(method.FullName()), filePath: file.Path(), name: string(method.Name()), input: string(method.Input().FullName()), output: string(method.Output().FullName()), clientStreaming: method.IsStreamingClient(), serverStreaming: method.IsStreamingServer(), location: descriptorLocation(file, method)}
			options, _ := method.Options().(*descriptorpb.MethodOptions)
			if options != nil && proto.HasExtension(options, httpExtensionType) {
				if methodState.clientStreaming || methodState.serverStreaming {
					return nil, protocolError("protocol_ir_invalid", "streaming_proxy_invalid", file.Path(), "", "HTTP proxy cannot target a streaming RPC")
				}
				value, ok := proto.GetExtension(options, httpExtensionType).(protoreflect.Message)
				if !ok || !value.IsValid() {
					return nil, protocolError("protocol_ir_invalid", "http_proxy_invalid", file.Path(), "", "HTTP proxy option is invalid")
				}
				proxy, err := decodeHTTPProxy(value, method)
				if err != nil {
					return nil, err
				}
				methodState.httpProxy = proxy
			}
			if options != nil && proto.HasExtension(options, rpcContextExtensionType) {
				value, ok := proto.GetExtension(options, rpcContextExtensionType).(protoreflect.Message)
				if !ok || !value.IsValid() {
					return nil, protocolError("protocol_ir_invalid", "rpc_context_invalid", file.Path(), "", "RPC context option is invalid")
				}
				rpcContext, err := decodeRPCContext(value, method)
				if err != nil {
					return nil, err
				}
				methodState.rpcContext = rpcContext
			}
			serviceState.methods = append(serviceState.methods, methodState)
			document.methods[methodState.fullName] = methodState
		}
		state.services = append(state.services, serviceState)
		document.services[serviceState.fullName] = serviceState
	}
	sort.Slice(state.messages, func(i, j int) bool { return state.messages[i].fullName < state.messages[j].fullName })
	sort.Slice(state.enums, func(i, j int) bool { return state.enums[i].fullName < state.enums[j].fullName })
	for _, service := range state.services {
		sort.Slice(service.methods, func(i, j int) bool { return service.methods[i].fullName < service.methods[j].fullName })
	}
	sort.Slice(state.services, func(i, j int) bool { return state.services[i].fullName < state.services[j].fullName })
	return state, nil
}

func projectMessage(message protoreflect.MessageDescriptor, file linker.File, parent *fileState, document *documentState) {
	if message.IsMapEntry() {
		return
	}
	state := &messageState{fullName: string(message.FullName()), filePath: file.Path(), location: descriptorLocation(file, message)}
	for i := 0; i < message.Fields().Len(); i++ {
		state.fields = append(state.fields, projectField(message.Fields().Get(i), file))
	}
	sort.Slice(state.fields, func(i, j int) bool { return state.fields[i].number < state.fields[j].number })
	parent.messages = append(parent.messages, state)
	document.messages[state.fullName] = state
	for i := 0; i < message.Messages().Len(); i++ {
		projectMessage(message.Messages().Get(i), file, parent, document)
	}
	for i := 0; i < message.Enums().Len(); i++ {
		projectEnum(message.Enums().Get(i), file, parent, document)
	}
}

func projectField(field protoreflect.FieldDescriptor, file linker.File) *fieldState {
	state := &fieldState{fullName: string(field.FullName()), filePath: file.Path(), name: string(field.Name()), jsonName: field.JSONName(), number: int(field.Number()), cardinality: CardinalitySingular, presence: PresenceImplicit, location: descriptorLocation(file, field)}
	if field.Cardinality() == protoreflect.Repeated {
		state.cardinality = CardinalityRepeated
	}
	if field.IsMap() {
		state.presence = PresenceMap
	} else if oneof := field.ContainingOneof(); oneof != nil && !oneof.IsSynthetic() {
		state.presence, state.oneof = PresenceOneof, string(oneof.Name())
	} else if field.HasOptionalKeyword() || field.HasPresence() && field.Kind() == protoreflect.MessageKind {
		state.presence = PresenceExplicit
	}
	state.typeValue = projectType(field)
	return state
}

func projectType(field protoreflect.FieldDescriptor) *typeState {
	if field.IsMap() {
		return &typeState{kind: TypeMap, key: projectSingularType(field.MapKey()), value: projectSingularType(field.MapValue())}
	}
	return projectSingularType(field)
}
func projectSingularType(field protoreflect.FieldDescriptor) *typeState {
	switch field.Kind() {
	case protoreflect.EnumKind:
		return &typeState{kind: TypeEnum, name: string(field.Enum().FullName())}
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return &typeState{kind: TypeMessage, name: string(field.Message().FullName())}
	default:
		return &typeState{kind: TypeScalar, name: field.Kind().String()}
	}
}

func projectEnum(enum protoreflect.EnumDescriptor, file linker.File, parent *fileState, document *documentState) {
	state := &enumState{fullName: string(enum.FullName()), filePath: file.Path(), location: descriptorLocation(file, enum)}
	for i := 0; i < enum.Values().Len(); i++ {
		value := enum.Values().Get(i)
		state.values = append(state.values, &enumValueState{name: string(value.Name()), number: int(value.Number()), location: descriptorLocation(file, value)})
	}
	parent.enums = append(parent.enums, state)
	document.enums[state.fullName] = state
}

func descriptorLocation(file linker.File, descriptor protoreflect.Descriptor) locationState {
	location := file.SourceLocations().ByDescriptor(descriptor)
	return locationState{file: file.Path(), line: location.StartLine + 1, column: location.StartColumn + 1}
}
