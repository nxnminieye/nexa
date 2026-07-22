package crudlogic

import (
	"fmt"
	"strings"

	"github.com/nxnminieye/nexa/generation/crudproto"
	"github.com/nxnminieye/nexa/generation/entity"
	"github.com/nxnminieye/nexa/nexaent"
)

func renderMutationBody(plan *planState, value entity.SnapshotEntity, method crudproto.Method, operation, methodName string) string {
	client := "l.svcCtx.DB." + value.Name()
	input, output := method.Input(), method.Output()
	tenant := isTenantEntity(plan, value)
	switch operation {
	case "create":
		var b strings.Builder
		if tenant {
			b.WriteString(renderTenantPrelude(plan, method))
		}
		createFields := mutableFields(value, nexaent.CRUDCreate)
		b.WriteString(renderMutationValidation(plan, input, value, createFields, false, methodName))
		fmt.Fprintf(&b, "\tmutation := %s.Create()\n", client)
		if tenant {
			b.WriteString("\tmutation.SetTenantID(tenantID)\n")
		}
		for _, field := range createFields {
			b.WriteString(renderCreateSetter(plan, input, field, methodName))
		}
		fmt.Fprintf(&b, "\trow, err := mutation.Save(l.ctx)\n\tif err != nil { return nil, %sProjectError(err) }\n\titem, err := %s(row)\n\tif err != nil { return nil, err }\n\treturn &pb.%s{%s: item}, nil\n", methodName, generatedHelperName(methodName, "toPB"), plan.protoMessageName(output), plan.protoFieldName(output, "item"))
		return b.String()
	case "update":
		identityPrelude, identity := renderIdentity(value, "in."+plan.protoFieldName(input, value.Identity().Name()), methodName)
		mask := "in." + plan.protoFieldName(input, "update_mask")
		var b strings.Builder
		b.WriteString(identityPrelude)
		if tenant {
			b.WriteString(renderTenantPrelude(plan, method))
		}
		fmt.Fprintf(&b, "\tif %s == nil || len(%s.Paths) == 0 { return nil, status.Error(codes.InvalidArgument, \"update_mask is required\") }\n", mask, mask)
		allowed := mutableFields(value, nexaent.CRUDUpdate)
		fmt.Fprintf(&b, "\tseen := make(map[string]struct{}, len(%s.Paths))\n\tfor _, field := range %s.Paths {\n\t\tif field == \"\" || strings.Contains(field, \".\") { return nil, status.Error(codes.InvalidArgument, \"update_mask contains unsupported field\") }\n\t\tif _, ok := seen[field]; ok { return nil, status.Error(codes.InvalidArgument, \"update_mask contains unsupported field\") }; seen[field] = struct{}{}\n\t\tswitch field {\n", mask, mask)
		for _, field := range allowed {
			fmt.Fprintf(&b, "\t\tcase %q:\n\t\t\tif in.%s == nil { return nil, status.Error(codes.InvalidArgument, \"update_mask contains unsupported field\") }\n", field.Name(), plan.protoFieldName(input, field.Name()))
		}
		b.WriteString("\t\tdefault: return nil, status.Error(codes.InvalidArgument, \"update_mask contains unsupported field\")\n\t\t}\n\t}\n")
		b.WriteString(renderMutationValidation(plan, input, value, allowed, true, methodName))
		predicates := "entitypkg." + exportedIdentifier(value.Identity().Name()) + "EQ(" + identity + ")"
		if tenant {
			predicates += ", entitypkg.TenantIDEQ(tenantID)"
		}
		fmt.Fprintf(&b, "\tcurrent, err := %s.Query().Where(%s).Only(l.ctx)\n\tif err != nil { return nil, %sProjectError(err) }\n\tmutation := %s.UpdateOne(current)\n\tfor _, field := range %s.Paths { switch field {\n", client, predicates, methodName, client, mask)
		for _, field := range allowed {
			fmt.Fprintf(&b, "\tcase %q:\n%s", field.Name(), renderUpdateSetter(plan, input, field, methodName))
		}
		b.WriteString("\t} }\n")
		fmt.Fprintf(&b, "\trow, err := mutation.Save(l.ctx)\n\tif err != nil { return nil, %sProjectError(err) }\n\titem, err := %s(row)\n\tif err != nil { return nil, err }\n\treturn &pb.%s{%s: item}, nil\n", methodName, generatedHelperName(methodName, "toPB"), plan.protoMessageName(output), plan.protoFieldName(output, "item"))
		return b.String()
	case "delete":
		identityPrelude, identity := renderIdentity(value, "in."+plan.protoFieldName(input, value.Identity().Name()), methodName)
		prelude := identityPrelude
		predicates := "entitypkg." + exportedIdentifier(value.Identity().Name()) + "EQ(" + identity + ")"
		if tenant {
			prelude += renderTenantPrelude(plan, method)
			predicates += ", entitypkg.TenantIDEQ(tenantID)"
		}
		return fmt.Sprintf("%s\tcurrent, err := %s.Query().Where(%s).Only(l.ctx)\n\tif err != nil { return nil, %sProjectError(err) }\n\tif err := %s.DeleteOne(current).Exec(l.ctx); err != nil { return nil, %sProjectError(err) }\n\treturn &pb.%s{}, nil\n", prelude, client, predicates, methodName, client, methodName, plan.protoMessageName(output))
	}
	return ""
}

func mutableFields(value entity.SnapshotEntity, operation nexaent.CRUDOperation) []entity.SnapshotField {
	var result []entity.SnapshotField
	for _, field := range value.Fields() {
		meta := field.Meta()
		if field.IsIdentity() || field.IsTenantField() || meta.CRUD == nil {
			continue
		}
		mutation := meta.CRUD.Mutation
		if operation == nexaent.CRUDCreate && (mutation == nexaent.MutationCreate || mutation == nexaent.MutationCreateUpdate) || operation == nexaent.CRUDUpdate && (mutation == nexaent.MutationUpdate || mutation == nexaent.MutationCreateUpdate) {
			result = append(result, field)
		}
	}
	return result
}

func renderCreateSetter(plan *planState, message string, field entity.SnapshotField, methodName string) string {
	goName, value := exportedIdentifier(field.Name()), "in."+plan.protoFieldName(message, field.Name())
	if mutationUsesValidatedValue(field) {
		if field.Optional() || field.Nillable() || field.HasDefault() {
			return fmt.Sprintf("\tif %s != nil { mutation.Set%s(%s) }\n", value, goName, mutationValueName(field))
		}
		return fmt.Sprintf("\tmutation.Set%s(%s)\n", goName, mutationValueName(field))
	}
	if field.Optional() || field.Nillable() || field.HasDefault() {
		if field.Type() == entity.ScalarBytes {
			return fmt.Sprintf("\tif %s != nil { mutation.Set%s(%s) }\n", value, goName, value)
		}
		return fmt.Sprintf("\tif %s != nil { mutation.Set%s(%s) }\n", value, goName, fromPBField(field, methodName, "*"+value))
	}
	return fmt.Sprintf("\tmutation.Set%s(%s)\n", goName, fromPBField(field, methodName, value))
}

func renderUpdateSetter(plan *planState, message string, field entity.SnapshotField, methodName string) string {
	value := "in." + plan.protoFieldName(message, field.Name())
	if field.Type() == entity.ScalarBytes {
		return fmt.Sprintf("\t\tmutation.Set%s(%s)\n", exportedIdentifier(field.Name()), value)
	}
	if mutationUsesValidatedValue(field) {
		return fmt.Sprintf("\t\tmutation.Set%s(%s)\n", exportedIdentifier(field.Name()), mutationValueName(field))
	}
	return fmt.Sprintf("\t\tmutation.Set%s(%s)\n", exportedIdentifier(field.Name()), fromPBField(field, methodName, "*"+value))
}

func fromPBField(field entity.SnapshotField, methodName, expression string) string {
	switch field.Type() {
	case entity.ScalarUUID:
		return expression
	case entity.ScalarEnum:
		return generatedHelperName(methodName, field.Name()+"_from_pb") + "(" + expression + ")"
	default:
		return expression
	}
}

func mutationValueName(field entity.SnapshotField) string {
	name := exportedIdentifier(field.Name())
	return strings.ToLower(name[:1]) + name[1:] + "Value"
}

func mutationUsesValidatedValue(field entity.SnapshotField) bool {
	switch field.Type() {
	case entity.ScalarInt64, entity.ScalarUint64, entity.ScalarUUID, entity.ScalarJSON, entity.ScalarTimestamp:
		return true
	default:
		return false
	}
}

func renderMutationValidation(plan *planState, message string, owner entity.SnapshotEntity, fields []entity.SnapshotField, update bool, methodName string) string {
	var b strings.Builder
	for _, field := range fields {
		if !mutationUsesValidatedValue(field) {
			continue
		}
		goName, variable := plan.protoFieldName(message, field.Name()), mutationValueName(field)
		condition := ""
		if update {
			condition = fmt.Sprintf("_, selected := seen[%q]; selected", field.Name())
		} else if field.Optional() || field.Nillable() || field.HasDefault() {
			condition = "in." + goName + " != nil"
		}
		witness := fmt.Sprintf("ent.%s{}.%s", owner.Name(), exportedIdentifier(field.Name()))
		if field.Nillable() && mutationNillableNeedsBase(field) {
			witness = generatedHelperName(methodName, "nillable_value") + "(" + witness + ")"
		}
		fmt.Fprintf(&b, "\tvar %s = %s\n", variable, witness)
		if condition != "" {
			fmt.Fprintf(&b, "\tif %s {\n", condition)
		}
		expression := "in." + goName
		if field.Optional() || field.Nillable() || field.HasDefault() || update {
			if field.Type() != entity.ScalarJSON && field.Type() != entity.ScalarTimestamp {
				expression = "*" + expression
			}
		}
		indent := "\t"
		if condition != "" {
			indent = "\t\t"
		}
		switch field.Type() {
		case entity.ScalarUUID:
			parsed, parseErr := variable+"Parsed", variable+"ParseErr"
			fmt.Fprintf(&b, "%s%s, %s := uuid.Parse(%s)\n%sif %s != nil || %s == uuid.Nil { return nil, status.Error(codes.InvalidArgument, \"invalid field value\") }\n%s%s = %s\n", indent, parsed, parseErr, expression, indent, parseErr, parsed, indent, variable, parsed)
		case entity.ScalarInt64:
			converted, conversionOK := variable+"Converted", variable+"ConversionOK"
			fmt.Fprintf(&b, "%s%s, %s := %s(%s, %s)\n%sif !%s { return nil, status.Error(codes.InvalidArgument, \"invalid field value\") }\n%s%s = %s\n", indent, converted, conversionOK, generatedHelperName(methodName, "checked_signed"), expression, witness, indent, conversionOK, indent, variable, converted)
		case entity.ScalarUint64:
			converted, conversionOK := variable+"Converted", variable+"ConversionOK"
			fmt.Fprintf(&b, "%s%s, %s := %s(%s, %s)\n%sif !%s { return nil, status.Error(codes.InvalidArgument, \"invalid field value\") }\n%s%s = %s\n", indent, converted, conversionOK, generatedHelperName(methodName, "checked_unsigned"), expression, witness, indent, conversionOK, indent, variable, converted)
		case entity.ScalarJSON:
			decoded, decodeErr := variable+"Decoded", variable+"DecodeErr"
			fmt.Fprintf(&b, "%sif %s == nil { return nil, status.Error(codes.InvalidArgument, \"invalid field value\") }\n%s%s, %s := %s(%s, %s)\n%sif %s != nil { return nil, status.Error(codes.InvalidArgument, \"invalid field value\") }\n%s%s = %s\n", indent, strings.TrimPrefix(expression, "*"), indent, decoded, decodeErr, generatedHelperName(methodName, "json_from_pb"), expression, witness, indent, decodeErr, indent, variable, decoded)
		case entity.ScalarTimestamp:
			fmt.Fprintf(&b, "%sif %s == nil || %s.CheckValid() != nil { return nil, status.Error(codes.InvalidArgument, \"invalid field value\") }\n%s%s = %s.AsTime()\n", indent, strings.TrimPrefix(expression, "*"), strings.TrimPrefix(expression, "*"), indent, variable, strings.TrimPrefix(expression, "*"))
		}
		if condition != "" {
			b.WriteString("\t}\n")
		}
	}
	return b.String()
}

func renderExactTypeHelpers(owner entity.SnapshotEntity, methodName, operation string) string {
	var needsSigned, needsUnsigned, needsJSON, needsNillableValue bool
	if operation == "get" || operation == "update" || operation == "delete" {
		needsSigned = owner.Identity().Type() == entity.ScalarInt64
		needsUnsigned = owner.Identity().Type() == entity.ScalarUint64
	}
	if operation == "create" || operation == "update" {
		crudOperation := map[string]nexaent.CRUDOperation{"create": nexaent.CRUDCreate, "update": nexaent.CRUDUpdate}[operation]
		for _, field := range mutableFields(owner, crudOperation) {
			needsSigned = needsSigned || field.Type() == entity.ScalarInt64
			needsUnsigned = needsUnsigned || field.Type() == entity.ScalarUint64
			needsJSON = needsJSON || field.Type() == entity.ScalarJSON
			needsNillableValue = needsNillableValue || field.Nillable() && mutationNillableNeedsBase(field)
		}
	}
	var b strings.Builder
	if needsNillableValue {
		fmt.Fprintf(&b, "func %s[T any](_ *T) T { var result T; return result }\n\n", generatedHelperName(methodName, "nillable_value"))
	}
	if needsSigned {
		fmt.Fprintf(&b, "func %s[T ~int | ~int8 | ~int16 | ~int32 | ~int64](value int64, _ T) (T, bool) { converted := T(value); return converted, int64(converted) == value }\n\n", generatedHelperName(methodName, "checked_signed"))
	}
	if needsUnsigned {
		fmt.Fprintf(&b, "func %s[T ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64](value uint64, _ T) (T, bool) { converted := T(value); return converted, uint64(converted) == value }\n\n", generatedHelperName(methodName, "checked_unsigned"))
	}
	if needsJSON {
		fmt.Fprintf(&b, "func %s[T any](value *structpb.Value, _ T) (T, error) { var result T; raw, err := value.MarshalJSON(); if err != nil { return result, err }; if err := json.Unmarshal(raw, &result); err != nil { return result, err }; return result, nil }\n\n", generatedHelperName(methodName, "json_from_pb"))
	}
	return b.String()
}

func mutationNillableNeedsBase(field entity.SnapshotField) bool {
	switch field.Type() {
	case entity.ScalarInt64, entity.ScalarUint64, entity.ScalarUUID, entity.ScalarTimestamp:
		return true
	default:
		return false
	}
}

func renderMutationConversionHelpers(plan *planState, owner entity.SnapshotEntity, methodName, operation string) string {
	var b strings.Builder
	for _, field := range mutableFields(owner, map[string]nexaent.CRUDOperation{"create": nexaent.CRUDCreate, "update": nexaent.CRUDUpdate}[operation]) {
		if field.Type() != entity.ScalarEnum {
			continue
		}
		function := generatedHelperName(methodName, field.Name()+"_from_pb")
		entType := "entitypkg." + exportedIdentifier(field.Name())
		pbType := "pb." + plan.protoEnumName(owner.Name(), field.Name())
		prefix := screamingSnake(plan.protoEnumProtoName(owner.Name(), field.Name()))
		fmt.Fprintf(&b, "func %s(value %s) %s { switch value {\n", function, pbType, entType)
		for _, item := range field.EnumValues() {
			protoValue := prefix + "_" + screamingSnake(item.Name)
			fmt.Fprintf(&b, "case pb.%s: return %s(%q)\n", plan.protoEnumValueName(owner.Name(), field.Name(), protoValue), entType, item.Value)
		}
		fmt.Fprintf(&b, "default: return %s(\"\") } }\n\n", entType)
	}
	return b.String()
}
