package crudlogic

import (
	"context"
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/bufbuild/protocompile"
	"github.com/gowebpki/jcs"
	"github.com/nxnminieye/nexa/generation/crudproto"
	"github.com/nxnminieye/nexa/generation/entity"
	genprotocol "github.com/nxnminieye/nexa/generation/protocol"
	"github.com/nxnminieye/nexa/provenance"
	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

var serviceIDPattern = regexp.MustCompile(`^[a-z][a-z0-9.-]*$`)

const serviceImportPlaceholder = "nexa.invalid/crudlogic/service"

func BuildPlan(verified crudproto.EntGraphPlan, layout ServiceLayout, options BuildOptions) (Plan, error) {
	serviceRoot, err := validateServiceLayout(layout)
	if err != nil {
		return Plan{}, err
	}
	verifiedService, err := verified.ServiceID()
	if err != nil || verifiedService != layout.ServiceID {
		return Plan{}, invalid("service_id_mismatch", "/layout/serviceId", err)
	}
	entities, err := verified.EntitySnapshot()
	if err != nil {
		return Plan{}, invalid("verified_snapshot_invalid", "/verified/entitySnapshot", err)
	}
	crud, err := verified.CRUDSnapshot()
	if err != nil {
		return Plan{}, invalid("verified_snapshot_invalid", "/verified/crudSnapshot", err)
	}
	var proto []byte
	var protoPath, pbImport, pbPackage, serviceImport string
	var protoNames protoGoNameSet
	if verified.HasCRUD() {
		proto, protoPath, err = verifiedProto(verified)
		if err != nil {
			return Plan{}, err
		}
		pbImport, pbPackage, protoNames, err = protoGoPackage(protoPath, proto)
		if err != nil {
			return Plan{}, err
		}
		if err := validateProtoGoNames(crud, protoNames); err != nil {
			return Plan{}, err
		}
		serviceImport = serviceImportPlaceholder
	}
	planDigest, err := verified.PlanDigest()
	if err != nil {
		return Plan{}, invalid("verified_snapshot_invalid", "/verified/planDigest", err)
	}
	sources, err := verified.Sources()
	if err != nil {
		return Plan{}, invalid("verified_snapshot_invalid", "/verified/sources", err)
	}
	refs := make([]provenance.SourceRef, len(sources))
	for i := range sources {
		refs[i] = sources[i].Ref
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].String() < refs[j].String() })
	state := &planState{layout: layout, serviceRoot: serviceRoot, serviceImport: serviceImport, pbImport: pbImport, pbPackage: pbPackage, verifiedDigest: planDigest, protoPath: protoPath, protoContent: proto, protoDigest: provenance.SHA256(proto), protoNames: protoNames, entitySnapshot: entities, crudSnapshot: crud}
	seenID, seenPath := map[string]bool{}, map[string]bool{}
	for _, service := range crud.Services() {
		entityName := strings.TrimSuffix(service.Name(), "CRUDService")
		entityValue, ok := entityByName(entities, entityName)
		if !ok || entityName == service.Name() {
			return Plan{}, invalid("crud_entity_invalid", "/verified/crudSnapshot/services", nil)
		}
		for _, method := range service.Methods() {
			operation := strings.ToLower(method.Name())
			if !validOperation(operation) {
				return Plan{}, invalid("crud_method_invalid", "/verified/crudSnapshot/services/methods", nil)
			}
			methodName := method.Name() + entityName
			id, artifactPath := logicArtifactIdentity(layout.ServiceID, layout.LogicRoot, methodName)
			content, renderErr := renderLogic(state, entityValue, method, methodName)
			if renderErr != nil {
				return Plan{}, renderErr
			}
			c := candidate{id: id, path: artifactPath, owner: generatorOwner, content: content, digest: provenance.SHA256(content), sources: append([]provenance.SourceRef(nil), refs...), manual: true, overwrite: options.OverwriteExisting}
			if err := appendCandidate(state, seenID, seenPath, c); err != nil {
				return Plan{}, err
			}
		}
	}
	if crud.HasTenantEntities() {
		content, renderErr := renderTenantHelper()
		if renderErr != nil {
			return Plan{}, renderErr
		}
		id, artifactPath := tenantHelperIdentity(layout)
		if err := appendCandidate(state, seenID, seenPath, candidate{id: id, path: artifactPath, owner: generatorOwner, content: content, digest: provenance.SHA256(content), sources: append([]provenance.SourceRef(nil), refs...)}); err != nil {
			return Plan{}, err
		}
	}
	sort.Slice(state.candidates, func(i, j int) bool { return state.candidates[i].id < state.candidates[j].id })
	wire := struct {
		ServiceID, EntSchemaDir, LogicRoot, VerifiedDigest, ProtoDigest string
		Overwrite                                                       bool
		Artifacts                                                       []struct {
			ID, Path, Digest string
			Manual           bool
		}
	}{ServiceID: layout.ServiceID, EntSchemaDir: layout.EntSchemaDir, LogicRoot: layout.LogicRoot, VerifiedDigest: planDigest.String(), ProtoDigest: state.protoDigest.String(), Overwrite: options.OverwriteExisting}
	for _, c := range state.candidates {
		wire.Artifacts = append(wire.Artifacts, struct {
			ID, Path, Digest string
			Manual           bool
		}{c.id, c.path, c.digest.String(), c.manual})
	}
	raw, _ := json.Marshal(wire)
	canonical, err := jcs.Transform(raw)
	if err != nil {
		return Plan{}, invalid("canonical_invalid", "/plan", err)
	}
	state.digest = provenance.SHA256(canonical)
	return Plan{state: state}, nil
}

func appendCandidate(state *planState, seenID, seenPath map[string]bool, value candidate) error {
	if seenID[value.id] || seenPath[value.path] {
		return invalid("derived_artifact_duplicate", "/artifacts", nil)
	}
	seenID[value.id], seenPath[value.path] = true, true
	state.candidates = append(state.candidates, value)
	return nil
}

func validateServiceLayout(layout ServiceLayout) (string, error) {
	if !serviceIDPattern.MatchString(layout.ServiceID) || !validRepoPath(layout.EntSchemaDir) || !validRepoPath(layout.LogicRoot) {
		return "", invalid("service_layout_invalid", "/layout", nil)
	}
	const entSuffix, logicSuffix = "/ent/schema", "/internal/logic"
	if !strings.HasSuffix(layout.EntSchemaDir, entSuffix) || !strings.HasSuffix(layout.LogicRoot, logicSuffix) {
		return "", invalid("service_layout_invalid", "/layout", nil)
	}
	entRoot := strings.TrimSuffix(layout.EntSchemaDir, entSuffix)
	logicRoot := strings.TrimSuffix(layout.LogicRoot, logicSuffix)
	if entRoot == "" || entRoot != logicRoot {
		return "", invalid("service_root_mismatch", "/layout/logicRoot", nil)
	}
	return entRoot, nil
}

func validRepoPath(value string) bool {
	return value != "" && path.Clean(value) == value && value != "." && !strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "../")
}
func validOperation(value string) bool {
	return value == "list" || value == "get" || value == "create" || value == "update" || value == "delete"
}
func logicArtifactIdentity(serviceID, root, method string) (string, string) {
	lower := strings.ToLower(method)
	return "crud-logic." + serviceID + "." + lower, path.Join(root, lower+"logic.go")
}

func verifiedProto(verified crudproto.EntGraphPlan) ([]byte, string, error) {
	a, err := verified.ProtoArtifact()
	if err != nil {
		return nil, "", invalid("proto_artifact_invalid", "/verified/proto", err)
	}
	b, err := a.Bytes()
	if err != nil {
		return nil, "", invalid("proto_artifact_invalid", "/verified/proto", err)
	}
	p, err := a.Path()
	if err != nil {
		return nil, "", invalid("proto_artifact_invalid", "/verified/proto", err)
	}
	return b, p, nil
}

func protoGoPackage(name string, content []byte) (string, string, protoGoNameSet, error) {
	resolver := &protocompile.SourceResolver{Accessor: protocompile.SourceAccessorFromMap(map[string]string{name: string(content), "nexa/protocol/v1/options.proto": string(genprotocol.OptionsProto())})}
	compiler := protocompile.Compiler{Resolver: protocompile.WithStandardImports(resolver)}
	files, err := compiler.Compile(context.Background(), name)
	if err != nil || len(files) != 1 {
		return "", "", protoGoNameSet{}, invalid("proto_artifact_invalid", "/verified/proto", err)
	}
	goPackage := files[0].Options().(*descriptorpb.FileOptions).GetGoPackage()
	parts := strings.Split(goPackage, ";")
	importPath, packageName := parts[0], path.Base(parts[0])
	if len(parts) == 2 {
		packageName = parts[1]
	}
	if len(parts) > 2 || importPath == "" || packageName == "" {
		return "", "", protoGoNameSet{}, invalid("go_package_invalid", "/verified/proto/goPackage", nil)
	}
	names, err := collectProtoGoNames(name, files[0], importPath, packageName)
	if err != nil {
		return "", "", protoGoNameSet{}, invalid("proto_artifact_invalid", "/verified/proto", err)
	}
	return importPath, packageName, names, nil
}

func collectProtoGoNames(name string, root protoreflect.FileDescriptor, targetImportPath, targetPackageName string) (protoGoNameSet, error) {
	request := &pluginpb.CodeGeneratorRequest{FileToGenerate: []string{name}}
	seen := make(map[string]bool)
	var appendFile func(protoreflect.FileDescriptor)
	appendFile = func(file protoreflect.FileDescriptor) {
		if file == nil || seen[file.Path()] {
			return
		}
		for index := 0; index < file.Imports().Len(); index++ {
			appendFile(file.Imports().Get(index).FileDescriptor)
		}
		seen[file.Path()] = true
		request.ProtoFile = append(request.ProtoFile, protodesc.ToFileDescriptorProto(file))
	}
	appendFile(root)
	var mappings []string
	for _, file := range request.ProtoFile {
		if file.GetOptions().GetGoPackage() != "" {
			continue
		}
		if file.GetName() != crudProtocolOptionsProtoPath {
			return protoGoNameSet{}, invalid("go_package_invalid", "/verified/proto/imports", nil)
		}
		mappings = append(mappings, "M"+crudProtocolOptionsProtoPath+"="+targetImportPath+";"+targetPackageName)
	}
	sort.Strings(mappings)
	parameter := strings.Join(mappings, ",")
	request.Parameter = &parameter
	plugin, err := (protogen.Options{}).New(request)
	if err != nil {
		return protoGoNameSet{}, err
	}
	target := plugin.FilesByPath[name]
	if target == nil || !target.Generate {
		return protoGoNameSet{}, invalid("proto_artifact_invalid", "/verified/proto", nil)
	}
	result := protoGoNameSet{messages: make(map[string]protoMessageGoNames, len(target.Messages)), enums: make(map[string]protoEnumGoNames, len(target.Enums))}
	for _, enum := range target.Enums {
		values := make(map[string]string, len(enum.Values))
		for _, value := range enum.Values {
			values[string(value.Desc.Name())] = value.GoIdent.GoName
		}
		result.enums[string(enum.Desc.Name())] = protoEnumGoNames{goName: enum.GoIdent.GoName, values: values}
	}
	for _, message := range target.Messages {
		fields := make(map[string]protoFieldGoNames, len(message.Fields))
		for _, field := range message.Fields {
			value := protoFieldGoNames{goName: field.GoName}
			if field.Enum != nil {
				value.enumName = string(field.Enum.Desc.Name())
			}
			fields[string(field.Desc.Name())] = value
		}
		result.messages[string(message.Desc.Name())] = protoMessageGoNames{goName: message.GoIdent.GoName, fields: fields}
	}
	return result, nil
}

func validateProtoGoNames(snapshot crudproto.Snapshot, names protoGoNameSet) error {
	enumNames := make(map[string]bool)
	for _, name := range snapshot.EnumNames() {
		enumNames[name] = true
		resolved, ok := names.enum(name)
		if !ok || resolved.goName == "" || len(resolved.values) == 0 {
			return invalid("proto_go_name_invalid", "/verified/proto/enums", nil)
		}
		for protoName, goName := range resolved.values {
			if protoName == "" || goName == "" {
				return invalid("proto_go_name_invalid", "/verified/proto/enums/values", nil)
			}
		}
	}
	for _, message := range snapshot.Messages() {
		resolved, ok := names.message(message.Name())
		if !ok || resolved.goName == "" {
			return invalid("proto_go_name_invalid", "/verified/proto/messages", nil)
		}
		for _, field := range message.Fields() {
			resolvedField, ok := names.field(message.Name(), field.Name())
			if !ok || resolvedField.goName == "" {
				return invalid("proto_go_name_invalid", "/verified/proto/messages/fields", nil)
			}
			if enumNames[field.Type()] {
				resolvedEnum, enumOK := names.enum(resolvedField.enumName)
				if resolvedField.enumName != field.Type() || !enumOK || resolvedEnum.goName == "" || len(resolvedEnum.values) == 0 {
					return invalid("proto_go_name_invalid", "/verified/proto/messages/fields/enum", nil)
				}
			} else if resolvedField.enumName != "" {
				return invalid("proto_go_name_invalid", "/verified/proto/messages/fields/enum", nil)
			}
		}
	}
	return nil
}

func entityByName(snapshot entity.Snapshot, name string) (entity.SnapshotEntity, bool) {
	for _, value := range snapshot.Entities() {
		if value.Name() == name {
			return value, true
		}
	}
	return entity.SnapshotEntity{}, false
}
