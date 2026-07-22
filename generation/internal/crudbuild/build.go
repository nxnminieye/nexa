package crudbuild

import (
	"bytes"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/nxnminieye/nexa/generation/entity"
	"github.com/nxnminieye/nexa/nexaent"
	"github.com/nxnminieye/nexa/provenance"
)

var (
	serviceIDPattern    = regexp.MustCompile(`^[a-z][a-z0-9.-]*$`)
	protoPackagePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)
	goPackagePattern    = regexp.MustCompile(`^[A-Za-z0-9_.~/-]+(;[A-Za-z_][A-Za-z0-9_]*)?$`)
	protoFieldPattern   = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	protoSymbolPattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

type desiredField struct {
	id, name, wireType      string
	preferred               int32
	repeated, optional      bool
	internal, tenantContext bool
	source                  provenance.Source
}

type desiredMessage struct {
	schemaID string
	id, name string
	fields   []desiredField
}

type desiredEntity struct {
	schemaID string
	messages []*desiredMessage
	service  *serviceState
	enums    []*enumState
}

func Build(entities entity.Document, spec Spec) (Document, LockProposal, error) {
	if entities.APIVersion() != entity.APIVersion {
		return Document{}, LockProposal{}, buildError("document_state_invalid", "/entities")
	}
	if !serviceIDPattern.MatchString(spec.ServiceID) {
		return Document{}, LockProposal{}, buildError("service_id_invalid", "/serviceId")
	}
	if !protoPackagePattern.MatchString(spec.ProtoPackage) {
		return Document{}, LockProposal{}, buildError("proto_package_invalid", "/protoPackage")
	}
	if !goPackagePattern.MatchString(spec.GoPackage) {
		return Document{}, LockProposal{}, buildError("go_package_invalid", "/goPackage")
	}
	if spec.ExistingLock != nil {
		if !spec.ExistingLock.Valid() {
			return Document{}, LockProposal{}, compatibilityError("lock_digest_mismatch", "/existingLock")
		}
		if spec.ExistingLock.ServiceID() != spec.ServiceID {
			return Document{}, LockProposal{}, compatibilityError("lock_service_mismatch", "/existingLock/serviceId")
		}
	}

	desired := make([]desiredEntity, 0)
	tenantEntityIDs := []string{}
	for entityIndex, item := range entities.Entities() {
		tenantField, hasTenantField := entityTenantField(item)
		if hasTenantField && !spec.MultiTenant.Enabled {
			return Document{}, LockProposal{}, buildError("multi_tenant_disabled", "/entities/"+itoa(entityIndex)+"/fields")
		}
		if hasTenantField {
			tenantEntityIDs = append(tenantEntityIDs, item.ID())
		}
		crud, optedIn := item.CRUD()
		if !optedIn {
			continue
		}
		built, err := buildEntity(item, crud, entityIndex, spec.MultiTenant.Enabled, tenantField, hasTenantField)
		if err != nil {
			return Document{}, LockProposal{}, err
		}
		desired = append(desired, built)
	}

	sort.Strings(tenantEntityIDs)
	documentState := &documentState{serviceID: spec.ServiceID, protoPackage: spec.ProtoPackage, goPackage: spec.GoPackage, imports: []string{}, enums: []*enumState{}, messages: []*messageState{}, services: []*serviceState{}, tenantEntityIDs: tenantEntityIDs, sources: []provenance.Source{}}
	if len(desired) == 0 {
		document, err := finalizeDocument(documentState)
		if err != nil {
			return Document{}, LockProposal{}, err
		}
		proposal := &lockProposalState{}
		if spec.ExistingLock != nil {
			proposal.before, proposal.after = spec.ExistingLock.state, spec.ExistingLock.state
			proposal.digest = provenance.SHA256(spec.ExistingLock.CanonicalJSON())
		}
		return document, LockProposal{state: proposal}, nil
	}

	var before *lockState
	if spec.ExistingLock != nil {
		before = spec.ExistingLock.state
	}
	after := cloneLockState(before)
	if after == nil {
		after = &lockState{serviceID: spec.ServiceID, schemas: []*lockSchemaState{}}
	}

	sourceSet := map[string]provenance.Source{}
	for _, item := range desired {
		schema := ensureSchema(after, item.schemaID)
		usedEnumNames := map[string]struct{}{}
		for _, message := range item.messages {
			for _, field := range message.fields {
				usedEnumNames[field.wireType] = struct{}{}
			}
		}
		desiredEnumIDs := map[string]struct{}{}
		for _, enum := range item.enums {
			if _, used := usedEnumNames[enum.name]; !used {
				continue
			}
			desiredEnumIDs[enum.id] = struct{}{}
			lockEnum := ensureLockEnum(schema, enum.id)
			projected, err := reconcileEnum(lockEnum, enum)
			if err != nil {
				return Document{}, LockProposal{}, err
			}
			documentState.enums = append(documentState.enums, projected)
		}
		for _, enum := range schema.enums {
			if _, current := desiredEnumIDs[enum.id]; !current && enum.active {
				retireEnum(enum)
			}
		}
		desiredIDs := make(map[string]struct{}, len(item.messages))
		for _, message := range item.messages {
			desiredIDs[message.id] = struct{}{}
			lockMessage := ensureLockMessage(schema, message.id)
			fields, err := reconcileMessage(lockMessage, message)
			if err != nil {
				return Document{}, LockProposal{}, err
			}
			documentState.messages = append(documentState.messages, &messageState{id: message.id, name: message.name, fields: fields, reservedNames: append([]string(nil), lockMessage.reservedNames...), reservedNumbers: append([]int32(nil), lockMessage.reservedNumbers...)})
			for _, field := range fields {
				sourceSet[field.source.Ref.String()] = field.source
			}
		}
		for _, message := range schema.messages {
			if _, current := desiredIDs[message.id]; !current && message.active {
				retireMessage(message)
			}
		}
		documentState.services = append(documentState.services, item.service)
	}
	for _, source := range sourceSet {
		documentState.sources = append(documentState.sources, source)
	}
	sort.SliceStable(documentState.enums, func(i, j int) bool { return documentState.enums[i].name < documentState.enums[j].name })
	sort.SliceStable(documentState.messages, func(i, j int) bool { return documentState.messages[i].name < documentState.messages[j].name })
	sort.SliceStable(documentState.services, func(i, j int) bool { return documentState.services[i].name < documentState.services[j].name })
	sort.SliceStable(documentState.sources, func(i, j int) bool {
		return documentState.sources[i].Ref.String() < documentState.sources[j].Ref.String()
	})
	documentState.imports = importsFor(documentState.messages)
	if servicesUseRPCContext(documentState.services) {
		documentState.imports = append(documentState.imports, "nexa/protocol/v1/options.proto")
		sort.Strings(documentState.imports)
	}
	if err := validateProtocolSymbols(documentState); err != nil {
		return Document{}, LockProposal{}, err
	}
	document, err := finalizeDocument(documentState)
	if err != nil {
		return Document{}, LockProposal{}, err
	}
	afterLock, err := finalizeLock(after)
	if err != nil {
		return Document{}, LockProposal{}, err
	}
	changed := before == nil || !bytes.Equal(before.canonical, afterLock.state.canonical)
	proposal := &lockProposalState{before: before, after: afterLock.state, digest: provenance.SHA256(afterLock.state.canonical), changed: changed}
	return document, LockProposal{state: proposal}, nil
}

func servicesUseRPCContext(services []*serviceState) bool {
	for _, service := range services {
		for _, method := range service.methods {
			if method.rpcContext != nil && len(method.rpcContext.contextFields) != 0 {
				return true
			}
		}
	}
	return false
}

func validateProtocolSymbols(state *documentState) error {
	symbols := map[string]struct{}{}
	register := func(name, pointer string) error {
		if !protoSymbolPattern.MatchString(name) {
			return renderError("proto_symbol_invalid", pointer)
		}
		if _, duplicate := symbols[name]; duplicate {
			return renderError("proto_symbol_duplicate", pointer)
		}
		symbols[name] = struct{}{}
		return nil
	}
	messageNames := map[string]struct{}{}
	for messageIndex, message := range state.messages {
		if err := register(message.name, "/messages/"+itoa(messageIndex)+"/name"); err != nil {
			return err
		}
		messageNames[message.name] = struct{}{}
		fieldNames, fieldNumbers := map[string]struct{}{}, map[int32]struct{}{}
		for fieldIndex, field := range message.fields {
			if _, duplicate := fieldNames[field.name]; duplicate {
				return wireError("wire_name_duplicate", "/messages/"+itoa(messageIndex)+"/fields/"+itoa(fieldIndex)+"/name")
			}
			if _, duplicate := fieldNumbers[field.number]; duplicate {
				return wireError("wire_number_duplicate", "/messages/"+itoa(messageIndex)+"/fields/"+itoa(fieldIndex)+"/number")
			}
			fieldNames[field.name], fieldNumbers[field.number] = struct{}{}, struct{}{}
		}
	}
	for enumIndex, enum := range state.enums {
		if err := register(enum.name, "/enums/"+itoa(enumIndex)+"/name"); err != nil {
			return err
		}
		numbers := map[int32]struct{}{}
		for valueIndex, value := range enum.values {
			pointer := "/enums/" + itoa(enumIndex) + "/values/" + itoa(valueIndex)
			if err := register(value.name, pointer+"/name"); err != nil {
				return err
			}
			if _, duplicate := numbers[value.number]; duplicate {
				return wireError("wire_number_duplicate", pointer+"/number")
			}
			numbers[value.number] = struct{}{}
		}
	}
	for serviceIndex, service := range state.services {
		if err := register(service.name, "/services/"+itoa(serviceIndex)+"/name"); err != nil {
			return err
		}
		methods := map[string]struct{}{}
		for methodIndex, method := range service.methods {
			pointer := "/services/" + itoa(serviceIndex) + "/methods/" + itoa(methodIndex)
			if !protoSymbolPattern.MatchString(method.name) {
				return renderError("proto_symbol_invalid", pointer+"/name")
			}
			if _, duplicate := methods[method.name]; duplicate {
				return renderError("proto_symbol_duplicate", pointer+"/name")
			}
			methods[method.name] = struct{}{}
			if _, ok := messageNames[method.input]; !ok {
				return renderError("proto_symbol_invalid", pointer+"/input")
			}
			if _, ok := messageNames[method.output]; !ok {
				return renderError("proto_symbol_invalid", pointer+"/output")
			}
		}
	}
	return nil
}

func buildEntity(item entity.Entity, crud nexaent.CRUDSpec, entityIndex int, multiTenantEnabled bool, tenantField entity.Field, hasTenantField bool) (desiredEntity, error) {
	identity := item.Identity()
	if !supportedIdentity(identity.Type()) {
		return desiredEntity{}, buildError("identity_type_unsupported", "/entities/"+itoa(entityIndex)+"/identity/type")
	}
	operations := crud.Operations()
	if len(operations) == 0 {
		return desiredEntity{}, buildError("crud_operation_invalid", "/entities/"+itoa(entityIndex)+"/crud/operations")
	}
	entityResult := desiredEntity{schemaID: item.ID(), enums: []*enumState{}}
	identityField := desiredField{id: item.ID() + "/identity:" + identity.Name(), name: identity.Name(), wireType: identityWireType(identity.Type()), preferred: 1, source: identity.Source()}
	itemFields := []desiredField{identityField}
	if identity.Kind() == entity.IdentityField {
		itemFields = nil
		for _, field := range item.Fields() {
			if field.IsIdentity() && field.Name() == identity.Name() && field.Type() == identity.Type() {
				identityField.id, identityField.source = field.ID(), field.Source()
				break
			}
		}
		if identityField.id == item.ID()+"/identity:"+identity.Name() {
			return desiredEntity{}, buildError("document_state_invalid", "/entities/"+itoa(entityIndex)+"/identity")
		}
	}
	createFields, updateFields := []desiredField{}, []desiredField{}
	for fieldIndex, field := range item.Fields() {
		if field.IsTenantField() {
			continue
		}
		wireType, enumValue, err := fieldWireType(item, field)
		if err != nil {
			return desiredEntity{}, buildError("field_type_unsupported", "/entities/"+itoa(entityIndex)+"/fields/"+itoa(fieldIndex)+"/type")
		}
		if enumValue != nil {
			entityResult.enums = append(entityResult.enums, enumValue)
		}
		meta := field.Meta()
		if meta.CRUD == nil {
			return desiredEntity{}, buildError("read_policy_conflict", "/entities/"+itoa(entityIndex)+"/fields/"+itoa(fieldIndex)+"/fieldMeta/crud")
		}
		projection := desiredField{id: field.ID(), name: field.Name(), wireType: wireType, optional: field.Nillable(), source: field.Source()}
		if field.IsIdentity() {
			if identity.Kind() != entity.IdentityField || field.ID() != identityField.id || meta.CRUD.Read != nexaent.ReadInclude {
				return desiredEntity{}, buildError("read_policy_conflict", "/entities/"+itoa(entityIndex)+"/fields/"+itoa(fieldIndex)+"/fieldMeta/crud/read")
			}
			if meta.CRUD.Mutation != nexaent.MutationNone {
				return desiredEntity{}, buildError("mutation_policy_conflict", "/entities/"+itoa(entityIndex)+"/fields/"+itoa(fieldIndex)+"/fieldMeta/crud/mutation")
			}
			identityField.wireType = wireType
			itemFields = append(itemFields, identityField)
			continue
		}
		if meta.CRUD.Read == nexaent.ReadInclude {
			if field.Sensitive() {
				return desiredEntity{}, buildError("read_policy_conflict", "/entities/"+itoa(entityIndex)+"/fields/"+itoa(fieldIndex)+"/fieldMeta/crud/read")
			}
			itemFields = append(itemFields, projection)
		} else if meta.CRUD.Read != nexaent.ReadExclude {
			return desiredEntity{}, buildError("read_policy_conflict", "/entities/"+itoa(entityIndex)+"/fields/"+itoa(fieldIndex)+"/fieldMeta/crud/read")
		}
		switch meta.CRUD.Mutation {
		case nexaent.MutationNone:
		case nexaent.MutationCreate:
			projection.optional = field.Optional() || field.Nillable() || field.HasDefault()
			createFields = append(createFields, projection)
		case nexaent.MutationUpdate:
			projection.optional = true
			updateFields = append(updateFields, projection)
		case nexaent.MutationCreateUpdate:
			createProjection := projection
			createProjection.optional = field.Optional() || field.Nillable() || field.HasDefault()
			createFields = append(createFields, createProjection)
			projection.optional = true
			updateFields = append(updateFields, projection)
		default:
			return desiredEntity{}, buildError("mutation_policy_conflict", "/entities/"+itoa(entityIndex)+"/fields/"+itoa(fieldIndex)+"/fieldMeta/crud/mutation")
		}
	}

	entityResult.messages = append(entityResult.messages, &desiredMessage{schemaID: item.ID(), id: messageID(item, item.Name()), name: item.Name(), fields: itemFields})
	methods := make([]*methodState, 0, len(operations))
	for operationIndex, operation := range operations {
		requestName, responseName := operationMessageNames(item.Name(), operation)
		var requestFields, responseFields []desiredField
		switch operation {
		case nexaent.CRUDList:
			requestFields = []desiredField{fixedField(item, operation, "offset", "uint64", 1), fixedField(item, operation, "limit", "uint64", 2)}
			responseFields = []desiredField{fixedRepeatedField(item, operation, "items", item.Name(), 1), fixedField(item, operation, "offset", "uint64", 2), fixedField(item, operation, "limit", "uint64", 3), fixedField(item, operation, "total", "uint64", 4)}
		case nexaent.CRUDGet:
			requestFields = []desiredField{identityField}
			responseFields = []desiredField{fixedField(item, operation, "item", item.Name(), 1)}
		case nexaent.CRUDCreate:
			requestFields = cloneDesiredFields(createFields)
			responseFields = []desiredField{fixedField(item, operation, "item", item.Name(), 1)}
		case nexaent.CRUDUpdate:
			requestFields = []desiredField{identityField, fixedField(item, operation, "update_mask", "google.protobuf.FieldMask", 2)}
			requestFields = append(requestFields, cloneDesiredFields(updateFields)...)
			responseFields = []desiredField{fixedField(item, operation, "item", item.Name(), 1)}
		case nexaent.CRUDDelete:
			requestFields = []desiredField{identityField}
			responseFields = []desiredField{}
		default:
			return desiredEntity{}, buildError("crud_operation_invalid", "/entities/"+itoa(entityIndex)+"/crud/operations/"+itoa(operationIndex))
		}
		var rpcContext *rpcContextState
		if multiTenantEnabled && hasTenantField {
			tenant := desiredField{id: item.ID() + "/operation:" + string(operation) + "/field:tenant_id", name: "tenant_id", wireType: "int64", source: tenantField.Source(), internal: true, tenantContext: true}
			requestFields = append(requestFields, tenant)
			rpcContext = &rpcContextState{contextFields: []*contextBindingState{{source: ContextTenantID, rpcField: "tenant_id"}}}
		} else {
			rpcContext = &rpcContextState{contextFields: []*contextBindingState{}}
		}
		entityResult.messages = append(entityResult.messages,
			&desiredMessage{schemaID: item.ID(), id: operationMessageID(item, operation, "request"), name: requestName, fields: requestFields},
			&desiredMessage{schemaID: item.ID(), id: operationMessageID(item, operation, "response"), name: responseName, fields: responseFields},
		)
		methods = append(methods, &methodState{id: item.ID() + "/operation:" + string(operation), name: titleOperation(operation), input: requestName, output: responseName, rpcContext: rpcContext})
	}
	entityResult.service = &serviceState{id: item.ID() + "/service:crud", name: item.Name() + "CRUDService", methods: methods}
	return entityResult, nil
}

func entityTenantField(item entity.Entity) (entity.Field, bool) {
	for _, field := range item.Fields() {
		if field.IsTenantField() {
			return field, true
		}
	}
	return entity.Field{}, false
}

func reconcileEnum(lockEnum *lockEnumState, desired *enumState) (*enumState, error) {
	byID := map[string]*enumAssignmentState{}
	occupied := map[int32]struct{}{}
	for _, value := range lockEnum.current {
		byID[value.valueID] = value
		occupied[value.number] = struct{}{}
	}
	for _, value := range lockEnum.retired {
		byID[value.valueID] = value
		occupied[value.number] = struct{}{}
	}
	desiredByID := make(map[string]*enumValueState, len(desired.values))
	for _, value := range desired.values {
		if _, duplicate := desiredByID[value.id]; duplicate {
			return nil, wireError("field_identity_duplicate", "/enums/"+desired.id+"/values")
		}
		desiredByID[value.id] = value
	}
	for _, current := range append([]*enumAssignmentState(nil), lockEnum.current...) {
		if _, keep := desiredByID[current.valueID]; !keep {
			retireEnumAssignment(lockEnum, current)
		}
	}
	lockEnum.current = nil
	values := append([]*enumValueState(nil), desired.values...)
	sort.SliceStable(values, func(i, j int) bool {
		if values[i].number == 0 || values[j].number == 0 {
			return values[i].number == 0
		}
		return values[i].id < values[j].id
	})
	result := &enumState{id: desired.id, name: desired.name}
	for index, value := range values {
		assignment := byID[value.id]
		if assignment != nil {
			if assignment.wireName != value.name || assignment.semantic != value.semantic || value.number == 0 && assignment.number != 0 {
				return nil, compatibilityError("wire_incompatible", "/enums/"+desired.id+"/values/"+itoa(index))
			}
			removeRetiredEnum(lockEnum, assignment.valueID)
			removeEnumReservation(lockEnum, assignment.wireName, assignment.number)
			assignment = cloneEnumAssignment(assignment)
		} else {
			if containsName(lockEnum.reservedNames, value.name) {
				return nil, compatibilityError("reservation_conflict", "/existingLock/reserved")
			}
			number := value.number
			if number == 0 {
				if _, unavailable := occupied[0]; unavailable || containsNumber(lockEnum.reservedNumbers, 0) {
					return nil, compatibilityError("reservation_conflict", "/existingLock/reserved")
				}
			} else {
				number = allocateEnumNumber(lockEnum, occupied)
				if number < 0 {
					return nil, wireError("wire_number_exhausted", "/enums/"+desired.id+"/values")
				}
			}
			assignment = &enumAssignmentState{valueID: value.id, wireName: value.name, semantic: value.semantic, number: number}
			occupied[number] = struct{}{}
		}
		lockEnum.current = append(lockEnum.current, assignment)
		result.values = append(result.values, &enumValueState{id: value.id, name: value.name, semantic: value.semantic, number: assignment.number})
	}
	lockEnum.active = true
	sort.SliceStable(lockEnum.current, func(i, j int) bool { return lockEnum.current[i].valueID < lockEnum.current[j].valueID })
	sort.SliceStable(result.values, func(i, j int) bool { return result.values[i].number < result.values[j].number })
	sort.Strings(lockEnum.reservedNames)
	sort.SliceStable(lockEnum.reservedNumbers, func(i, j int) bool { return lockEnum.reservedNumbers[i] < lockEnum.reservedNumbers[j] })
	result.reservedNames = append([]string(nil), lockEnum.reservedNames...)
	result.reservedNumbers = append([]int32(nil), lockEnum.reservedNumbers...)
	return result, nil
}

func retireEnum(value *lockEnumState) {
	for _, assignment := range append([]*enumAssignmentState(nil), value.current...) {
		retireEnumAssignment(value, assignment)
	}
	value.current = nil
	value.active = false
}

func retireEnumAssignment(value *lockEnumState, assignment *enumAssignmentState) {
	if !containsEnumAssignment(value.retired, assignment.valueID) {
		value.retired = append(value.retired, cloneEnumAssignment(assignment))
	}
	if !containsName(value.reservedNames, assignment.wireName) {
		value.reservedNames = append(value.reservedNames, assignment.wireName)
	}
	if !containsNumber(value.reservedNumbers, assignment.number) {
		value.reservedNumbers = append(value.reservedNumbers, assignment.number)
	}
}

func allocateEnumNumber(value *lockEnumState, occupied map[int32]struct{}) int32 {
	for number := int32(1); number < 2147483647; number++ {
		if _, used := occupied[number]; !used && !containsNumber(value.reservedNumbers, number) {
			return number
		}
	}
	return -1
}

func ensureLockEnum(schema *lockSchemaState, id string) *lockEnumState {
	for _, value := range schema.enums {
		if value.id == id {
			return value
		}
	}
	value := &lockEnumState{id: id, current: []*enumAssignmentState{}, retired: []*enumAssignmentState{}, reservedNames: []string{}, reservedNumbers: []int32{}}
	schema.enums = append(schema.enums, value)
	return value
}

func reconcileMessage(lockMessage *lockMessageState, desired *desiredMessage) ([]*fieldState, error) {
	byID := map[string]*assignmentState{}
	occupied := map[int32]struct{}{}
	for _, value := range lockMessage.current {
		byID[value.fieldID] = value
		occupied[value.number] = struct{}{}
	}
	for _, value := range lockMessage.retired {
		byID[value.fieldID] = value
		occupied[value.number] = struct{}{}
	}
	desiredByID := make(map[string]desiredField, len(desired.fields))
	for index, field := range desired.fields {
		if !protoFieldPattern.MatchString(field.name) {
			return nil, wireError("wire_name_invalid", "/messages/"+desired.id+"/fields/"+itoa(index)+"/name")
		}
		if _, duplicate := desiredByID[field.id]; duplicate {
			return nil, wireError("field_identity_duplicate", "/messages/"+desired.id+"/fields/"+itoa(index)+"/id")
		}
		desiredByID[field.id] = field
	}
	for _, current := range append([]*assignmentState(nil), lockMessage.current...) {
		if _, keep := desiredByID[current.fieldID]; !keep {
			retireAssignment(lockMessage, current)
		}
	}
	lockMessage.current = nil
	fields := make([]*fieldState, 0, len(desired.fields))
	sort.SliceStable(desired.fields, func(i, j int) bool {
		left, right := desired.fields[i], desired.fields[j]
		if left.preferred > 0 || right.preferred > 0 {
			if left.preferred == 0 {
				return false
			}
			if right.preferred == 0 {
				return true
			}
			if left.preferred != right.preferred {
				return left.preferred < right.preferred
			}
		}
		return left.id < right.id
	})
	for index, field := range desired.fields {
		assignment := byID[field.id]
		if assignment != nil {
			if assignment.wireName != field.name || assignment.wireType != field.wireType || field.preferred > 0 && assignment.number != field.preferred {
				return nil, compatibilityError("wire_incompatible", "/messages/"+desired.id+"/fields/"+itoa(index)+"/type")
			}
			removeRetired(lockMessage, assignment.fieldID)
			removeReservation(lockMessage, assignment.wireName, assignment.number)
			assignment = cloneAssignment(assignment)
			assignment.source = field.source
		} else {
			if containsName(lockMessage.reservedNames, field.name) {
				return nil, compatibilityError("reservation_conflict", "/existingLock/reserved")
			}
			number := field.preferred
			if number > 0 {
				_, used := occupied[number]
				if used || !legalNumber(number) || containsNumber(lockMessage.reservedNumbers, number) {
					return nil, compatibilityError("reservation_conflict", "/existingLock/reserved")
				}
			} else {
				number = allocateNumber(lockMessage, occupied)
				if number == 0 {
					return nil, wireError("wire_number_exhausted", "/messages/"+desired.id+"/fields")
				}
			}
			assignment = &assignmentState{fieldID: field.id, wireName: field.name, wireType: field.wireType, number: number, source: field.source}
			occupied[number] = struct{}{}
		}
		lockMessage.current = append(lockMessage.current, assignment)
		fields = append(fields, &fieldState{id: field.id, name: field.name, wireType: field.wireType, number: assignment.number, repeated: field.repeated, optional: field.optional, internal: field.internal, tenantContext: field.tenantContext, source: field.source})
	}
	lockMessage.active = true
	sort.SliceStable(lockMessage.current, func(i, j int) bool { return lockMessage.current[i].fieldID < lockMessage.current[j].fieldID })
	sort.SliceStable(fields, func(i, j int) bool { return fields[i].number < fields[j].number })
	sort.Strings(lockMessage.reservedNames)
	sort.SliceStable(lockMessage.reservedNumbers, func(i, j int) bool { return lockMessage.reservedNumbers[i] < lockMessage.reservedNumbers[j] })
	return fields, nil
}

func retireMessage(message *lockMessageState) {
	for _, assignment := range append([]*assignmentState(nil), message.current...) {
		retireAssignment(message, assignment)
	}
	message.current = nil
	message.active = false
}

func retireAssignment(message *lockMessageState, assignment *assignmentState) {
	if !containsAssignment(message.retired, assignment.fieldID) {
		message.retired = append(message.retired, cloneAssignment(assignment))
	}
	if !containsName(message.reservedNames, assignment.wireName) {
		message.reservedNames = append(message.reservedNames, assignment.wireName)
	}
	if !containsNumber(message.reservedNumbers, assignment.number) {
		message.reservedNumbers = append(message.reservedNumbers, assignment.number)
	}
}

func allocateNumber(message *lockMessageState, occupied map[int32]struct{}) int32 {
	for number := int32(1); number <= 536870911; number++ {
		if number >= 19000 && number <= 19999 {
			number = 19999
			continue
		}
		if _, used := occupied[number]; !used && !containsNumber(message.reservedNumbers, number) {
			return number
		}
	}
	return 0
}

func ensureSchema(lock *lockState, id string) *lockSchemaState {
	for _, value := range lock.schemas {
		if value.id == id {
			return value
		}
	}
	value := &lockSchemaState{id: id, enums: []*lockEnumState{}, messages: []*lockMessageState{}}
	lock.schemas = append(lock.schemas, value)
	return value
}

func ensureLockMessage(schema *lockSchemaState, id string) *lockMessageState {
	for _, value := range schema.messages {
		if value.id == id {
			return value
		}
	}
	value := &lockMessageState{id: id, current: []*assignmentState{}, retired: []*assignmentState{}, reservedNames: []string{}, reservedNumbers: []int32{}}
	schema.messages = append(schema.messages, value)
	return value
}

func cloneLockState(input *lockState) *lockState {
	if input == nil {
		return nil
	}
	result := &lockState{serviceID: input.serviceID, schemas: make([]*lockSchemaState, len(input.schemas)), canonical: append([]byte(nil), input.canonical...)}
	for i, schema := range input.schemas {
		copySchema := &lockSchemaState{id: schema.id, enums: make([]*lockEnumState, len(schema.enums)), messages: make([]*lockMessageState, len(schema.messages))}
		for j, enum := range schema.enums {
			copyEnum := &lockEnumState{id: enum.id, active: enum.active, reservedNames: append([]string(nil), enum.reservedNames...), reservedNumbers: append([]int32(nil), enum.reservedNumbers...)}
			for _, value := range enum.current {
				copyEnum.current = append(copyEnum.current, cloneEnumAssignment(value))
			}
			for _, value := range enum.retired {
				copyEnum.retired = append(copyEnum.retired, cloneEnumAssignment(value))
			}
			copySchema.enums[j] = copyEnum
		}
		for j, message := range schema.messages {
			copyMessage := &lockMessageState{id: message.id, active: message.active, reservedNames: append([]string(nil), message.reservedNames...), reservedNumbers: append([]int32(nil), message.reservedNumbers...)}
			for _, value := range message.current {
				copyMessage.current = append(copyMessage.current, cloneAssignment(value))
			}
			for _, value := range message.retired {
				copyMessage.retired = append(copyMessage.retired, cloneAssignment(value))
			}
			copySchema.messages[j] = copyMessage
		}
		result.schemas[i] = copySchema
	}
	return result
}

func cloneAssignment(value *assignmentState) *assignmentState { copied := *value; return &copied }
func cloneEnumAssignment(value *enumAssignmentState) *enumAssignmentState {
	copied := *value
	return &copied
}
func cloneDesiredFields(values []desiredField) []desiredField {
	return append([]desiredField(nil), values...)
}

func fieldWireType(owner entity.Entity, field entity.Field) (string, *enumState, error) {
	switch field.Type() {
	case entity.ScalarBool:
		return "bool", nil, nil
	case entity.ScalarInt64:
		return "int64", nil, nil
	case entity.ScalarUint64:
		return "uint64", nil, nil
	case entity.ScalarFloat:
		return "float", nil, nil
	case entity.ScalarDouble:
		return "double", nil, nil
	case entity.ScalarString:
		return "string", nil, nil
	case entity.ScalarBytes:
		return "bytes", nil, nil
	case entity.ScalarTimestamp:
		return "google.protobuf.Timestamp", nil, nil
	case entity.ScalarUUID:
		return "string", nil, nil
	case entity.ScalarJSON:
		return "google.protobuf.Value", nil, nil
	case entity.ScalarEnum:
		name := owner.Name() + exportedName(field.Name())
		if !protoSymbolPattern.MatchString(name) {
			return "", nil, buildError("field_type_unsupported", "")
		}
		enum := &enumState{id: field.ID() + "/enum", name: name, values: []*enumValueState{{id: field.ID() + "/enum-value:unspecified", name: screamingSnake(name) + "_UNSPECIFIED", number: 0}}}
		seen := map[string]struct{}{enum.values[0].name: {}}
		for _, value := range field.EnumValues() {
			symbol := screamingSnake(name) + "_" + screamingSnake(value.Name)
			if !protoSymbolPattern.MatchString(symbol) {
				return "", nil, buildError("field_type_unsupported", "")
			}
			if _, duplicate := seen[symbol]; duplicate {
				return "", nil, buildError("field_type_unsupported", "")
			}
			seen[symbol] = struct{}{}
			enum.values = append(enum.values, &enumValueState{id: field.ID() + "/enum-value:" + value.Name, name: symbol, semantic: value.Value, number: -1})
		}
		return name, enum, nil
	default:
		return "", nil, buildError("field_type_unsupported", "")
	}
}

func supportedIdentity(value entity.ScalarType) bool {
	return value == entity.ScalarInt64 || value == entity.ScalarUint64 || value == entity.ScalarString || value == entity.ScalarUUID
}
func identityWireType(value entity.ScalarType) string {
	if value == entity.ScalarUUID {
		return "string"
	}
	return string(value)
}
func messageID(item entity.Entity, name string) string { return item.ID() + "/message:" + name }
func operationMessageID(item entity.Entity, operation nexaent.CRUDOperation, side string) string {
	return item.ID() + "/operation:" + string(operation) + "/message:" + side
}
func operationMessageNames(entityName string, operation nexaent.CRUDOperation) (string, string) {
	prefix := titleOperation(operation) + entityName
	return prefix + "Request", prefix + "Response"
}
func titleOperation(operation nexaent.CRUDOperation) string {
	value := string(operation)
	return strings.ToUpper(value[:1]) + value[1:]
}
func fixedField(item entity.Entity, operation nexaent.CRUDOperation, name, typ string, number int32) desiredField {
	return desiredField{id: item.ID() + "/operation:" + string(operation) + "/field:" + name, name: name, wireType: typ, preferred: number, source: item.Source()}
}
func fixedRepeatedField(item entity.Entity, operation nexaent.CRUDOperation, name, typ string, number int32) desiredField {
	value := fixedField(item, operation, name, typ, number)
	value.repeated = true
	return value
}

func importsFor(messages []*messageState) []string {
	set := map[string]struct{}{}
	for _, message := range messages {
		for _, field := range message.fields {
			switch field.wireType {
			case "google.protobuf.Timestamp":
				set["google/protobuf/timestamp.proto"] = struct{}{}
			case "google.protobuf.Value":
				set["google/protobuf/struct.proto"] = struct{}{}
			case "google.protobuf.FieldMask":
				set["google/protobuf/field_mask.proto"] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func removeRetired(message *lockMessageState, id string) {
	result := message.retired[:0]
	for _, value := range message.retired {
		if value.fieldID != id {
			result = append(result, value)
		}
	}
	message.retired = result
}
func removeRetiredEnum(value *lockEnumState, id string) {
	result := value.retired[:0]
	for _, assignment := range value.retired {
		if assignment.valueID != id {
			result = append(result, assignment)
		}
	}
	value.retired = result
}
func removeEnumReservation(value *lockEnumState, name string, number int32) {
	names := value.reservedNames[:0]
	for _, item := range value.reservedNames {
		if item != name {
			names = append(names, item)
		}
	}
	value.reservedNames = names
	numbers := value.reservedNumbers[:0]
	for _, item := range value.reservedNumbers {
		if item != number {
			numbers = append(numbers, item)
		}
	}
	value.reservedNumbers = numbers
}
func containsEnumAssignment(values []*enumAssignmentState, id string) bool {
	for _, value := range values {
		if value.valueID == id {
			return true
		}
	}
	return false
}
func removeReservation(message *lockMessageState, name string, number int32) {
	names := message.reservedNames[:0]
	for _, value := range message.reservedNames {
		if value != name {
			names = append(names, value)
		}
	}
	message.reservedNames = names
	numbers := message.reservedNumbers[:0]
	for _, value := range message.reservedNumbers {
		if value != number {
			numbers = append(numbers, value)
		}
	}
	message.reservedNumbers = numbers
}
func containsAssignment(values []*assignmentState, id string) bool {
	for _, value := range values {
		if value.fieldID == id {
			return true
		}
	}
	return false
}
func containsName(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
func containsNumber(values []int32, wanted int32) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
func exportedName(value string) string {
	var result strings.Builder
	upper := true
	for _, r := range value {
		if r == '_' || r == '-' {
			upper = true
			continue
		}
		if upper {
			result.WriteRune(unicode.ToUpper(r))
			upper = false
		} else {
			result.WriteRune(r)
		}
	}
	return result.String()
}
func screamingSnake(value string) string {
	var result strings.Builder
	for index, r := range value {
		if r == '-' || r == ' ' {
			result.WriteByte('_')
			continue
		}
		if unicode.IsUpper(r) && index > 0 {
			previous := rune(value[index-1])
			if previous != '_' && !unicode.IsUpper(previous) {
				result.WriteByte('_')
			}
		}
		result.WriteRune(unicode.ToUpper(r))
	}
	return result.String()
}
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := [20]byte{}
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}
