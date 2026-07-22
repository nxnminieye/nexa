package crudlogic

import (
	"bytes"
	"fmt"
	"go/format"
	"strings"
	"unicode"

	"github.com/nxnminieye/nexa/generation/crudproto"
	"github.com/nxnminieye/nexa/generation/entity"
)

var initialisms = map[string]string{
	"acl": "ACL", "api": "API", "ascii": "ASCII", "aws": "AWS", "cpu": "CPU", "css": "CSS", "dns": "DNS", "eof": "EOF", "gb": "GB", "guid": "GUID",
	"hcl": "HCL", "html": "HTML", "http": "HTTP", "https": "HTTPS", "id": "ID", "ip": "IP", "json": "JSON", "kb": "KB", "lhs": "LHS", "mac": "MAC",
	"mb": "MB", "qps": "QPS", "ram": "RAM", "rhs": "RHS", "rpc": "RPC", "sla": "SLA", "smtp": "SMTP", "sql": "SQL", "ssh": "SSH", "sso": "SSO",
	"tcp": "TCP", "tls": "TLS", "ttl": "TTL", "udp": "UDP", "ui": "UI", "uid": "UID", "uri": "URI", "url": "URL", "utf8": "UTF8", "uuid": "UUID",
	"vm": "VM", "xml": "XML", "xmpp": "XMPP", "xsrf": "XSRF", "xss": "XSS",
}

func exportedIdentifier(value string) string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == '_' || r == '-' || r == '.' })
	var out strings.Builder
	for _, part := range parts {
		lower := strings.ToLower(part)
		if known := initialisms[lower]; known != "" {
			out.WriteString(known)
			continue
		}
		runes := []rune(part)
		if len(runes) == 0 {
			continue
		}
		runes[0] = unicode.ToUpper(runes[0])
		out.WriteString(string(runes))
	}
	return out.String()
}

func renderLogic(plan *planState, value entity.SnapshotEntity, method crudproto.Method, methodName string) ([]byte, error) {
	var body string
	operation := strings.ToLower(method.Name())
	switch operation {
	case "list", "get":
		body = renderReadBody(plan, value, method, operation, methodName)
	case "create", "update", "delete":
		body = renderMutationBody(plan, value, method, operation, methodName)
	default:
		return nil, invalid("crud_method_invalid", "/verified/crudSnapshot/methods", nil)
	}
	imports := renderImports(plan, value, operation, method)
	var out bytes.Buffer
	fmt.Fprintf(&out, "package logic\n\nimport (\n%s)\n\n", imports)
	fmt.Fprintf(&out, "type %sLogic struct {\n\tctx context.Context\n\tsvcCtx *svc.ServiceContext\n\tlogx.Logger\n}\n\n", methodName)
	fmt.Fprintf(&out, "func New%sLogic(ctx context.Context, svcCtx *svc.ServiceContext) *%sLogic {\n\treturn &%sLogic{ctx: ctx, svcCtx: svcCtx, Logger: logx.WithContext(ctx)}\n}\n\n", methodName, methodName, methodName)
	fmt.Fprintf(&out, "func (l *%sLogic) %s(in *pb.%s) (*pb.%s, error) {\n%s}\n\n", methodName, methodName, plan.protoMessageName(method.Input()), plan.protoMessageName(method.Output()), body)
	if operation != "delete" {
		out.WriteString(renderPBMapper(plan, value, methodName))
	}
	if operation == "create" || operation == "update" {
		out.WriteString(renderMutationConversionHelpers(plan, value, methodName, operation))
	}
	out.WriteString(renderExactTypeHelpers(value, methodName, operation))
	out.WriteString(renderErrorProjection(methodName))
	formatted, err := format.Source(out.Bytes())
	if err != nil {
		return nil, invalid("logic_render_invalid", "/artifacts/"+strings.ToLower(methodName), err)
	}
	return formatted, nil
}

func renderImports(plan *planState, value entity.SnapshotEntity, operation string, method crudproto.Method) string {
	imports := []string{"\t\"context\"\n", "\t\"errors\"\n", fmt.Sprintf("\tpb %q\n", plan.pbImport), fmt.Sprintf("\t%q\n", plan.serviceImport+"/ent"), fmt.Sprintf("\t%q\n", plan.serviceImport+"/internal/svc"), "\t\"github.com/zeromicro/go-zero/core/logx\"\n", "\t\"google.golang.org/grpc/codes\"\n", "\t\"google.golang.org/grpc/status\"\n"}
	if operation == "get" || operation == "update" || operation == "delete" || operation == "list" && isTenantEntity(plan, value) || operation != "delete" && mapperNeedsEnum(value) || operation == "create" && mutationNeedsType(value, operation, entity.ScalarEnum) {
		imports = append(imports, fmt.Sprintf("\tentitypkg %q\n", plan.serviceImport+"/ent/"+strings.ToLower(value.Name())))
	}
	if operation == "list" {
		imports = append(imports, "\t\"math\"\n")
	}
	if operationNeedsUUIDPackage(value, operation) {
		imports = append(imports, "\t\"github.com/google/uuid\"\n")
	}
	if hasTenantContext(method) {
		imports = append(imports, fmt.Sprintf("\t%q\n", plan.serviceImport+"/internal/logic/crudtenant"))
	}
	if operation == "update" {
		imports = append(imports, "\t\"strings\"\n")
	}
	if operation != "delete" && mapperNeedsTimestamp(value) || mutationNeedsType(value, operation, entity.ScalarTimestamp) {
		imports = append(imports, "\t\"google.golang.org/protobuf/types/known/timestamppb\"\n")
	}
	if operation != "delete" && mapperNeedsStruct(value) || mutationNeedsType(value, operation, entity.ScalarJSON) {
		imports = append(imports, "\t\"google.golang.org/protobuf/types/known/structpb\"\n")
	}
	if operation != "delete" && mapperNeedsStruct(value) || mutationNeedsType(value, operation, entity.ScalarJSON) {
		imports = append(imports, "\t\"encoding/json\"\n")
	}
	sortStrings(imports)
	return strings.Join(imports, "")
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
func hasTenantContext(method crudproto.Method) bool {
	for _, b := range method.RPCContext().ContextFields() {
		if b.Source() == crudproto.ContextTenantID {
			return true
		}
	}
	return false
}

func generatedHelperName(methodName, role string) string {
	return strings.ToLower(methodName[:1]) + methodName[1:] + exportedIdentifier(role)
}

func renderPBMapper(plan *planState, value entity.SnapshotEntity, methodName string) string {
	mapper := generatedHelperName(methodName, "toPB")
	messageName := value.Name()
	var b strings.Builder
	fmt.Fprintf(&b, "func %s(value *ent.%s) (*pb.%s, error) {\n\tif value == nil { return nil, nil }\n", mapper, value.Name(), plan.protoMessageName(messageName))
	for _, field := range value.Fields() {
		meta := field.Meta()
		if field.IsIdentity() || field.IsTenantField() || meta.CRUD == nil || meta.CRUD.Read != "include" {
			continue
		}
		fieldExpression := "value." + exportedIdentifier(field.Name())
		local := mutationValueName(field)
		switch field.Type() {
		case entity.ScalarJSON:
			fmt.Fprintf(&b, "\t%s, err := %s(%s)\n\tif err != nil { return nil, status.Error(codes.Internal, \"crud operation failed\") }\n", local, generatedHelperName(methodName, "json_to_pb"), fieldExpression)
		case entity.ScalarTimestamp:
			if field.Nillable() {
				fmt.Fprintf(&b, "\tvar %s *timestamppb.Timestamp\n\tif %s != nil { %s = timestamppb.New(*%s); if %s.CheckValid() != nil { return nil, status.Error(codes.Internal, \"crud operation failed\") } }\n", local, fieldExpression, local, fieldExpression, local)
			} else {
				fmt.Fprintf(&b, "\t%s := timestamppb.New(%s)\n\tif %s.CheckValid() != nil { return nil, status.Error(codes.Internal, \"crud operation failed\") }\n", local, fieldExpression, local)
			}
		}
	}
	fmt.Fprintf(&b, "\treturn &pb.%s{\n", plan.protoMessageName(messageName))
	identity := value.Identity()
	identityExpression := entToPB(identity.Type(), "value."+exportedIdentifier(identity.Name()))
	fmt.Fprintf(&b, "\t\t%s: %s,\n", plan.protoFieldName(messageName, identity.Name()), identityExpression)
	for _, field := range value.Fields() {
		meta := field.Meta()
		if field.IsIdentity() || field.IsTenantField() || meta.CRUD == nil || meta.CRUD.Read != "include" {
			continue
		}
		expression := entToPB(field.Type(), "value."+exportedIdentifier(field.Name()))
		if field.Type() == entity.ScalarJSON {
			expression = mutationValueName(field)
		} else if field.Type() == entity.ScalarTimestamp {
			expression = mutationValueName(field)
		} else if field.Type() == entity.ScalarUUID && field.Nillable() {
			expression = generatedHelperName(methodName, "uuid") + "(value." + exportedIdentifier(field.Name()) + ")"
		} else if field.Type() == entity.ScalarInt64 && field.Nillable() {
			expression = generatedHelperName(methodName, "signed_to_pb") + "(value." + exportedIdentifier(field.Name()) + ")"
		} else if field.Type() == entity.ScalarUint64 && field.Nillable() {
			expression = generatedHelperName(methodName, "unsigned_to_pb") + "(value." + exportedIdentifier(field.Name()) + ")"
		} else if field.Type() == entity.ScalarBytes && field.Nillable() {
			expression = generatedHelperName(methodName, "bytes_to_pb") + "(value." + exportedIdentifier(field.Name()) + ")"
		} else if field.Type() == entity.ScalarEnum {
			expression = generatedHelperName(methodName, field.Name()+"_to_pb") + "(value." + exportedIdentifier(field.Name()) + ")"
		}
		fmt.Fprintf(&b, "\t\t%s: %s,\n", plan.protoFieldName(messageName, field.Name()), expression)
	}
	b.WriteString("\t}, nil\n}\n\n")
	if mapperNeedsStruct(value) {
		fmt.Fprintf(&b, "func %s(value any) (*structpb.Value, error) { raw, err := json.Marshal(value); if err != nil { return nil, err }; result := new(structpb.Value); if err := result.UnmarshalJSON(raw); err != nil { return nil, err }; return result, nil }\n\n", generatedHelperName(methodName, "json_to_pb"))
	}
	if mapperNeedsNillableUUID(value) {
		fmt.Fprintf(&b, "func %s(value *uuid.UUID) *string { if value == nil { return nil }; result := value.String(); return &result }\n\n", generatedHelperName(methodName, "uuid"))
	}
	if mapperNeedsNillableType(value, entity.ScalarInt64) {
		fmt.Fprintf(&b, "func %s[T ~int | ~int8 | ~int16 | ~int32 | ~int64](value *T) *int64 { if value == nil { return nil }; result := int64(*value); return &result }\n\n", generatedHelperName(methodName, "signed_to_pb"))
	}
	if mapperNeedsNillableType(value, entity.ScalarUint64) {
		fmt.Fprintf(&b, "func %s[T ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64](value *T) *uint64 { if value == nil { return nil }; result := uint64(*value); return &result }\n\n", generatedHelperName(methodName, "unsigned_to_pb"))
	}
	if mapperNeedsNillableType(value, entity.ScalarBytes) {
		fmt.Fprintf(&b, "func %s(value *[]byte) []byte { if value == nil { return nil }; return *value }\n\n", generatedHelperName(methodName, "bytes_to_pb"))
	}
	for _, field := range value.Fields() {
		if field.Meta().CRUD == nil || field.Meta().CRUD.Read != "include" || field.Type() != entity.ScalarEnum {
			continue
		}
		b.WriteString(renderEnumToPB(plan, value, field, methodName))
	}
	return b.String()
}

func entToPB(typ entity.ScalarType, expression string) string {
	switch typ {
	case entity.ScalarInt64:
		return "int64(" + expression + ")"
	case entity.ScalarUint64:
		return "uint64(" + expression + ")"
	case entity.ScalarTimestamp:
		return "timestamppb.New(" + expression + ")"
	case entity.ScalarUUID:
		return expression + ".String()"
	case entity.ScalarJSON:
		return expression
	default:
		return expression
	}
}
func mapperNeedsTimestamp(v entity.SnapshotEntity) bool {
	for _, f := range v.Fields() {
		if f.Type() == entity.ScalarTimestamp && f.Meta().CRUD != nil && f.Meta().CRUD.Read == "include" {
			return true
		}
	}
	return false
}
func mapperNeedsStruct(v entity.SnapshotEntity) bool {
	for _, f := range v.Fields() {
		if f.Type() == entity.ScalarJSON && f.Meta().CRUD != nil && f.Meta().CRUD.Read == "include" {
			return true
		}
	}
	return false
}
func mapperNeedsEnum(v entity.SnapshotEntity) bool {
	for _, f := range v.Fields() {
		if f.Type() == entity.ScalarEnum && f.Meta().CRUD != nil && f.Meta().CRUD.Read == "include" {
			return true
		}
	}
	return false
}
func mapperNeedsNillableUUID(v entity.SnapshotEntity) bool {
	for _, f := range v.Fields() {
		if f.Type() == entity.ScalarUUID && f.Nillable() && f.Meta().CRUD != nil && f.Meta().CRUD.Read == "include" {
			return true
		}
	}
	return false
}

func mapperNeedsNillableType(value entity.SnapshotEntity, typ entity.ScalarType) bool {
	for _, field := range value.Fields() {
		if field.Type() == typ && field.Nillable() && field.Meta().CRUD != nil && field.Meta().CRUD.Read == "include" {
			return true
		}
	}
	return false
}
func operationUsesType(v entity.SnapshotEntity, operation string, typ entity.ScalarType) bool {
	if operation != "create" && v.Identity().Type() == typ {
		return true
	}
	if operation == "create" || operation == "update" {
		for _, f := range v.Fields() {
			if f.Type() == typ && f.Meta().CRUD != nil && (operation == "create" && (f.Meta().CRUD.Mutation == "create" || f.Meta().CRUD.Mutation == "create-update") || operation == "update" && (f.Meta().CRUD.Mutation == "update" || f.Meta().CRUD.Mutation == "create-update")) {
				return true
			}
		}
	}
	return false
}

func mutationNeedsType(v entity.SnapshotEntity, operation string, typ entity.ScalarType) bool {
	return operationUsesType(v, operation, typ)
}

func operationNeedsUUIDPackage(value entity.SnapshotEntity, operation string) bool {
	if operation == "get" || operation == "update" || operation == "delete" {
		if value.Identity().Type() == entity.ScalarUUID {
			return true
		}
	}
	if operation == "create" || operation == "update" {
		if mutationNeedsType(value, operation, entity.ScalarUUID) {
			return true
		}
	}
	return operation != "delete" && mapperNeedsNillableUUID(value)
}

func screamingSnake(value string) string {
	var result strings.Builder
	runes := []rune(value)
	for index, r := range runes {
		if r == '-' || r == ' ' {
			result.WriteByte('_')
			continue
		}
		if unicode.IsUpper(r) && index > 0 && runes[index-1] != '_' && !unicode.IsUpper(runes[index-1]) {
			result.WriteByte('_')
		}
		result.WriteRune(unicode.ToUpper(r))
	}
	return result.String()
}

func renderEnumToPB(plan *planState, owner entity.SnapshotEntity, field entity.SnapshotField, methodName string) string {
	function := generatedHelperName(methodName, field.Name()+"_to_pb")
	entType := "entitypkg." + exportedIdentifier(field.Name())
	pbType := "pb." + plan.protoEnumName(owner.Name(), field.Name())
	var b strings.Builder
	if field.Nillable() {
		fmt.Fprintf(&b, "func %s(value *%s) *%s { if value == nil { return nil }; result := %s(*value); return &result }\n", function, entType, pbType, function+"Value")
		function += "Value"
	}
	fmt.Fprintf(&b, "func %s(value %s) %s { switch string(value) {\n", function, entType, pbType)
	prefix := screamingSnake(plan.protoEnumProtoName(owner.Name(), field.Name()))
	for _, item := range field.EnumValues() {
		protoValue := prefix + "_" + screamingSnake(item.Name)
		fmt.Fprintf(&b, "case %q: return pb.%s\n", item.Value, plan.protoEnumValueName(owner.Name(), field.Name(), protoValue))
	}
	fmt.Fprintf(&b, "default: return pb.%s } }\n\n", plan.protoEnumValueName(owner.Name(), field.Name(), prefix+"_UNSPECIFIED"))
	return b.String()
}
