package entityload

import (
	"fmt"
	"sort"

	"entgo.io/ent/entc/gen"
	entfield "entgo.io/ent/schema/field"
	"github.com/nxnminieye/nexa/generation/entmixin"
	"github.com/nxnminieye/nexa/generation/internal/entityvalue"
	"github.com/nxnminieye/nexa/generation/sourcecomment"
	"github.com/nxnminieye/nexa/provenance"
)

type sourceResolver func(position string) (provenance.DomainSource, error)

func projectGraph(graph *gen.Graph, facts sourcecomment.FactGraph, moduleSources []provenance.Source, resolve sourceResolver) (entityvalue.Projection, error) {
	if graph == nil {
		return entityvalue.Projection{}, fmt.Errorf("entity graph is unavailable")
	}
	nodes := append([]*gen.Type(nil), graph.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	projection := entityvalue.Projection{
		Entities:               make([]entityvalue.EntityProjection, 0, len(nodes)),
		ExecutionModuleSources: append([]provenance.Source(nil), moduleSources...),
	}
	selected := make(map[*gen.Type]bool, len(nodes))
	for _, node := range nodes {
		if node == nil {
			continue
		}
		_, hasSchemaFact := facts.Fact(sourcecomment.FactID{SemanticID: node.Name, Key: "scope"})
		_, hasCRUDFact := facts.Fact(sourcecomment.FactID{SemanticID: node.Name, Key: "crud.operations"})
		selected[node] = hasSchemaFact || hasCRUDFact
	}
	type boundField struct {
		owner  *gen.Type
		field  *gen.Field
		direct bool
	}
	boundByEdge := make(map[*gen.Edge][]boundField)
	for _, owner := range nodes {
		if owner == nil {
			continue
		}
		fields := append([]*gen.Field(nil), owner.Fields...)
		if owner.ID != nil && owner.ID.UserDefined {
			fields = append(fields, owner.ID)
		}
		for _, field := range fields {
			if field == nil || !field.IsEdgeField() {
				continue
			}
			edge, err := field.Edge()
			if err != nil {
				return entityvalue.Projection{}, fmt.Errorf("bound edge for %s.%s is invalid: %w", owner.Name, field.Name, err)
			}
			boundByEdge[edge] = append(boundByEdge[edge], boundField{owner: owner, field: field, direct: true})
			if edge.Ref != nil {
				boundByEdge[edge.Ref] = append(boundByEdge[edge.Ref], boundField{owner: owner, field: field})
			}
		}
	}
	for _, node := range nodes {
		if node == nil || node.Name == "" {
			return entityvalue.Projection{}, fmt.Errorf("entity name is invalid")
		}
		_, present := facts.Fact(sourcecomment.FactID{SemanticID: node.Name, Key: "scope"})
		crud, hasCRUD, err := facts.CRUD(node.Name)
		if err != nil {
			return entityvalue.Projection{}, err
		}
		if !present && !hasCRUD {
			continue
		}
		if !present {
			return entityvalue.Projection{}, fmt.Errorf("schema facts are required")
		}
		meta, err := facts.SchemaFacts(node.Name)
		if err != nil {
			return entityvalue.Projection{}, err
		}
		file, err := resolve(node.Pos())
		if err != nil {
			return entityvalue.Projection{}, err
		}
		entityRef, err := provenance.RepositoryRef(file.String(), "schema:"+node.Name)
		if err != nil {
			return entityvalue.Projection{}, err
		}
		if node.IsView() || node.HasCompositeID() || node.ID == nil {
			return entityvalue.Projection{}, fmt.Errorf("entity identity is unsupported")
		}
		identityType, ok := scalarType(node.ID.Type)
		if !ok {
			return entityvalue.Projection{}, fmt.Errorf("entity identity type is unsupported")
		}
		entityIndex := len(projection.Entities)
		if hasCRUD && !crudExactTypeSupported(node.ID.Type) {
			return entityvalue.Projection{}, entityvalue.UnsupportedFieldType(fmt.Sprintf("/entities/%d/identity", entityIndex))
		}
		identity := entityvalue.IdentityProjection{Kind: "implicit", Name: "id", Type: "int64"}
		if node.ID.UserDefined {
			identity = entityvalue.IdentityProjection{Kind: "field", Name: node.ID.Name, Type: identityType}
		} else if node.ID.Type == nil || node.ID.Type.Type != entfield.TypeInt {
			return entityvalue.Projection{}, fmt.Errorf("implicit entity identity is unsupported")
		}
		entityProjection := entityvalue.EntityProjection{Name: node.Name, SourceRef: entityRef, Meta: meta, Identity: identity}
		if hasCRUD {
			entityProjection.CRUD = &crud
		}
		fields := append([]*gen.Field(nil), node.Fields...)
		if node.ID.UserDefined {
			fields = append(fields, node.ID)
		}
		sort.Slice(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })
		entityProjection.Fields = make([]entityvalue.FieldProjection, 0, len(fields))
		seen := make(map[string]struct{}, len(fields))
		for fieldIndex, field := range fields {
			if field == nil || field.Name == "" {
				return entityvalue.Projection{}, fmt.Errorf("field name is invalid")
			}
			if _, duplicate := seen[field.Name]; duplicate {
				return entityvalue.Projection{}, fmt.Errorf("field name is duplicated")
			}
			seen[field.Name] = struct{}{}
			fieldMeta, err := facts.FieldFacts(node.Name + "." + field.Name)
			if err != nil {
				return entityvalue.Projection{}, err
			}
			isTenantField := false
			if field.Annotations != nil {
				if value, present := field.Annotations[entmixin.FieldAnnotationName]; present {
					metadata, metadataErr := entmixin.DecodeFieldAnnotation(value)
					if metadataErr != nil {
						return entityvalue.Projection{}, metadataErr
					}
					isTenantField = metadata.Tenant
				}
			}
			typeID, ok := scalarType(field.Type)
			if !ok {
				return entityvalue.Projection{}, fmt.Errorf("field type is unsupported")
			}
			isIdentity := field == node.ID && node.ID.UserDefined
			if hasCRUD && !isIdentity && fieldParticipatesInCRUD(fieldMeta, isTenantField, crud) && !crudExactTypeSupported(field.Type) {
				return entityvalue.Projection{}, entityvalue.UnsupportedFieldType(fmt.Sprintf("/entities/%d/fields/%d/type", entityIndex, fieldIndex))
			}
			fieldRef, err := provenance.RepositoryRef(file.String(), "schema:"+node.Name+"/field:"+field.Name)
			if err != nil {
				return entityvalue.Projection{}, err
			}
			enums, err := projectEnums(field)
			if err != nil {
				return entityvalue.Projection{}, err
			}
			entityProjection.Fields = append(entityProjection.Fields, entityvalue.FieldProjection{
				Name: field.Name, SourceRef: fieldRef, Type: typeID, EnumValues: enums,
				Optional: field.Optional, Nillable: field.Nillable, Immutable: field.Immutable,
				HasDefault: field.Default, Sensitive: field.Sensitive(), IsIdentity: isIdentity,
				IsTenantField: isTenantField,
				Meta:          fieldMeta,
			})
		}
		edges := append([]*gen.Edge(nil), node.Edges...)
		sort.Slice(edges, func(i, j int) bool { return edges[i].Name < edges[j].Name })
		entityProjection.Edges = make([]entityvalue.EdgeProjection, 0, len(edges))
		for _, edge := range edges {
			if edge == nil || edge.Name == "" || edge.Type == nil {
				return entityvalue.Projection{}, fmt.Errorf("entity edge is invalid")
			}
			if edge.Through != nil {
				return entityvalue.Projection{}, fmt.Errorf("edge %s.%s through is unsupported", node.Name, edge.Name)
			}
			if edge.Immutable {
				return entityvalue.Projection{}, fmt.Errorf("edge %s.%s immutable is unsupported", node.Name, edge.Name)
			}
			if len(edge.Annotations) != 0 {
				return entityvalue.Projection{}, fmt.Errorf("edge %s.%s custom annotations are unsupported", node.Name, edge.Name)
			}
			if !selected[edge.Type] {
				return entityvalue.Projection{}, fmt.Errorf("edge %s.%s target is not projected", node.Name, edge.Name)
			}
			inverseName := ""
			if edge.Ref != nil {
				if edge.Ref.Ref != edge {
					return entityvalue.Projection{}, fmt.Errorf("edge %s.%s inverse is not closed", node.Name, edge.Name)
				}
				inverseName = edge.Ref.Name
			} else if edge.IsInverse() {
				return entityvalue.Projection{}, fmt.Errorf("edge %s.%s inverse is not closed", node.Name, edge.Name)
			}
			direction := "to"
			boundFieldID := ""
			bindings := boundByEdge[edge]
			if len(bindings) > 1 {
				return entityvalue.Projection{}, fmt.Errorf("edge %s.%s has multiple bound fields", node.Name, edge.Name)
			}
			if len(bindings) == 1 {
				binding := bindings[0]
				switch {
				case binding.owner == node && (binding.owner != edge.Type || binding.direct):
					direction = "to"
				case binding.owner == edge.Type:
					direction = "from"
				default:
					return entityvalue.Projection{}, fmt.Errorf("edge %s.%s bound field owner is invalid", node.Name, edge.Name)
				}
				boundFieldID = "schema:" + bindings[0].owner.Name + "/field:" + bindings[0].field.Name
			} else if edge.IsInverse() {
				direction = "from"
			}
			edgeRef, err := provenance.RepositoryRef(file.String(), "schema:"+node.Name+"/edge:"+edge.Name)
			if err != nil {
				return entityvalue.Projection{}, err
			}
			entityProjection.Edges = append(entityProjection.Edges, entityvalue.EdgeProjection{Name: edge.Name, SourceRef: edgeRef, TargetEntityID: "schema:" + edge.Type.Name, Direction: direction, InverseName: inverseName, BoundFieldID: boundFieldID, Optional: edge.Optional, Unique: edge.Unique})
		}
		projection.Entities = append(projection.Entities, entityProjection)
	}
	return projection, nil
}

func crudExactTypeSupported(info *entfield.TypeInfo) bool {
	if info == nil {
		return false
	}
	switch info.Type {
	case entfield.TypeJSON:
		return true
	case entfield.TypeUUID:
		const googleUUID = "github.com/google/uuid"
		return info.Ident == "uuid.UUID" && info.PkgPath == googleUUID && info.RType != nil &&
			info.RType.Name == "UUID" && info.RType.Ident == "uuid.UUID" && info.RType.PkgPath == googleUUID
	default:
		return info.RType == nil
	}
}

func fieldParticipatesInCRUD(meta sourcecomment.FieldFacts, tenant bool, crud sourcecomment.CRUDOperations) bool {
	if tenant {
		return false
	}
	if meta.CRUD == nil {
		return false
	}
	for _, operation := range crud.Operations() {
		switch operation {
		case sourcecomment.CRUDList, sourcecomment.CRUDGet:
			if meta.CRUD.Read == sourcecomment.ReadInclude {
				return true
			}
		case sourcecomment.CRUDCreate:
			if meta.CRUD.Read == sourcecomment.ReadInclude || meta.CRUD.Mutation == sourcecomment.MutationCreate || meta.CRUD.Mutation == sourcecomment.MutationCreateUpdate {
				return true
			}
		case sourcecomment.CRUDUpdate:
			if meta.CRUD.Read == sourcecomment.ReadInclude || meta.CRUD.Mutation == sourcecomment.MutationUpdate || meta.CRUD.Mutation == sourcecomment.MutationCreateUpdate {
				return true
			}
		}
	}
	return false
}

func scalarType(info *entfield.TypeInfo) (string, bool) {
	if info == nil {
		return "", false
	}
	switch info.Type {
	case entfield.TypeBool:
		return "bool", true
	case entfield.TypeInt8, entfield.TypeInt16, entfield.TypeInt32, entfield.TypeInt, entfield.TypeInt64:
		return "int64", true
	case entfield.TypeUint8, entfield.TypeUint16, entfield.TypeUint32, entfield.TypeUint, entfield.TypeUint64:
		return "uint64", true
	case entfield.TypeFloat32:
		return "float", true
	case entfield.TypeFloat64:
		return "double", true
	case entfield.TypeString:
		return "string", true
	case entfield.TypeBytes:
		return "bytes", true
	case entfield.TypeTime:
		return "timestamp", true
	case entfield.TypeUUID:
		return "uuid", true
	case entfield.TypeJSON:
		return "json", true
	case entfield.TypeEnum:
		return "enum", true
	default:
		return "", false
	}
}

func projectEnums(field *gen.Field) ([]entityvalue.EnumValue, error) {
	if field.Type.Type != entfield.TypeEnum {
		if len(field.Enums) != 0 {
			return nil, fmt.Errorf("enum values on non-enum field")
		}
		return []entityvalue.EnumValue{}, nil
	}
	if len(field.Enums) == 0 {
		return nil, fmt.Errorf("enum values are required")
	}
	result := make([]entityvalue.EnumValue, len(field.Enums))
	for index, value := range field.Enums {
		if value.Name == "" || value.Value == "" {
			return nil, fmt.Errorf("enum value is invalid")
		}
		result[index] = entityvalue.EnumValue{Name: value.Name, Value: value.Value}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			return result[i].Value < result[j].Value
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}
