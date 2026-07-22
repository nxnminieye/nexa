package crudlogic

import (
	"fmt"

	"github.com/nxnminieye/nexa/generation/crudproto"
	"github.com/nxnminieye/nexa/generation/entity"
)

func renderReadBody(plan *planState, value entity.SnapshotEntity, method crudproto.Method, operation, methodName string) string {
	entityName, client := value.Name(), "l.svcCtx.DB."+value.Name()
	input, output := method.Input(), method.Output()
	tenant := isTenantEntity(plan, value)
	switch operation {
	case "list":
		predicate := ""
		prelude := ""
		if tenant {
			prelude = renderTenantPrelude(plan, method)
			predicate = ".Where(entitypkg.TenantIDEQ(tenantID))"
		}
		offset := "in." + plan.protoFieldName(input, "offset")
		limit := "in." + plan.protoFieldName(input, "limit")
		responseType := plan.protoMessageName(output)
		itemsField := plan.protoFieldName(output, "items")
		offsetField := plan.protoFieldName(output, "offset")
		limitField := plan.protoFieldName(output, "limit")
		totalField := plan.protoFieldName(output, "total")
		entityType := plan.protoMessageName(entityName)
		return fmt.Sprintf("%s\tif %s > uint64(math.MaxInt) || %s > uint64(math.MaxInt) { return nil, status.Error(codes.InvalidArgument, \"invalid pagination\") }\n\tquery := %s.Query()%s\n\ttotal, err := query.Clone().Count(l.ctx)\n\tif err != nil { return nil, %sProjectError(err) }\n\tif %s == 0 { return &pb.%s{%s: []*pb.%s{}, %s: %s, %s: %s, %s: uint64(total)}, nil }\n\trows, err := query.Offset(int(%s)).Limit(int(%s)).All(l.ctx)\n\tif err != nil { return nil, %sProjectError(err) }\n\titems := make([]*pb.%s, len(rows))\n\tfor i, row := range rows { item, err := %s(row); if err != nil { return nil, err }; items[i] = item }\n\treturn &pb.%s{%s: items, %s: %s, %s: %s, %s: uint64(total)}, nil\n", prelude, offset, limit, client, predicate, methodName, limit, responseType, itemsField, entityType, offsetField, offset, limitField, limit, totalField, offset, limit, methodName, entityType, generatedHelperName(methodName, "toPB"), responseType, itemsField, offsetField, offset, limitField, limit, totalField)
	case "get":
		identityPrelude, identity := renderIdentity(value, "in."+plan.protoFieldName(input, value.Identity().Name()), methodName)
		predicates := "entitypkg." + exportedIdentifier(value.Identity().Name()) + "EQ(" + identity + ")"
		prelude := identityPrelude
		if tenant {
			prelude += renderTenantPrelude(plan, method)
			predicates += ", entitypkg.TenantIDEQ(tenantID)"
		}
		return fmt.Sprintf("%s\trow, err := %s.Query().Where(%s).Only(l.ctx)\n\tif err != nil { return nil, %sProjectError(err) }\n\titem, err := %s(row)\n\tif err != nil { return nil, err }\n\treturn &pb.%s{%s: item}, nil\n", prelude, client, predicates, methodName, generatedHelperName(methodName, "toPB"), plan.protoMessageName(output), plan.protoFieldName(output, "item"))
	}
	return ""
}

func renderTenantPrelude(plan *planState, method crudproto.Method) string {
	for _, binding := range method.RPCContext().ContextFields() {
		if binding.Source() == crudproto.ContextTenantID {
			return fmt.Sprintf("\ttenantID, err := crudtenant.RequireTenantID(in.%s)\n\tif err != nil { return nil, err }\n", plan.protoFieldName(method.Input(), binding.RPCField()))
		}
	}
	return ""
}

func isTenantEntity(plan *planState, value entity.SnapshotEntity) bool {
	for _, id := range plan.crudSnapshot.TenantEntityIDs() {
		if id == value.ID() {
			return true
		}
	}
	return false
}

func renderIdentity(value entity.SnapshotEntity, expression, methodName string) (string, string) {
	id := value.Identity()
	invalid := "return nil, status.Error(codes.InvalidArgument, \"invalid identity\")"
	switch id.Type() {
	case entity.ScalarInt64:
		return fmt.Sprintf("\tidentity, identityOK := %s(%s, ent.%s{}.%s)\n\tif !identityOK || identity <= 0 { %s }\n", generatedHelperName(methodName, "checked_signed"), expression, value.Name(), exportedIdentifier(id.Name()), invalid), "identity"
	case entity.ScalarUint64:
		return fmt.Sprintf("\tidentity, identityOK := %s(%s, ent.%s{}.%s)\n\tif !identityOK || identity == 0 { %s }\n", generatedHelperName(methodName, "checked_unsigned"), expression, value.Name(), exportedIdentifier(id.Name()), invalid), "identity"
	case entity.ScalarString:
		return fmt.Sprintf("\tif %s == \"\" { %s }\n", expression, invalid), expression
	case entity.ScalarUUID:
		return fmt.Sprintf("\tidentity, err := uuid.Parse(%s)\n\tif err != nil || identity == uuid.Nil { %s }\n", expression, invalid), "identity"
	default:
		return "\treturn nil, status.Error(codes.InvalidArgument, \"invalid identity\")\n", expression
	}
}
