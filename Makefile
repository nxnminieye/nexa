.PHONY: test vet build consumer skills adoption-contracts contracts generation-contracts generated-check source-contracts service-bundles runtime check

test:
	GOWORK=off go test ./... -timeout 30m

vet:
	GOWORK=off go vet ./...

build:
	mkdir -p .tmp/bin
	GOWORK=off go build -o .tmp/bin/nexactl ./cmd/nexactl

consumer:
	cd fixtures/consumers/plugin-composition && GOWORK=off go test ./...

skills:
	GOWORK=off GOTOOLCHAIN=local GOENV=off go test ./plugins/nexactl/governance -count=1
	GOWORK=off GOTOOLCHAIN=local GOENV=off go test ./integration -run '^TestReferenceNexactlGovernanceSkillValidation$$' -count=1

adoption-contracts:
	GOWORK=off GOTOOLCHAIN=local GOENV=off go test ./internal/adoption/... -count=1
	GOWORK=off GOTOOLCHAIN=local GOENV=off go test ./integration -run '^TestAdoptionLocalConsumer$$' -count=1

contracts:
	GOWORK=off GOTOOLCHAIN=local GOENV=off go test ./provenance ./project/servicecatalog ./generation/artifact ./generation/api ./runtime/buildinfo -count=1
	GOWORK=off GOTOOLCHAIN=local GOENV=off go test ./integration -run 'TestBusinessContractExternalConsumer|TestAPIContractDoesNotChangeMinimalHostComposition|TestReferenceNexactlInspect' -count=1

generation-contracts:
	GOWORK=off GOTOOLCHAIN=local GOENV=off go test ./nexaent ./generation/... -count=1

generated-check: generation-contracts
	GOWORK=off GOTOOLCHAIN=local GOENV=off go test ./plugins/nexactl/generation -count=1
	GOWORK=off GOTOOLCHAIN=local GOENV=off go test ./integration -run 'TestEntityIRExternalConsumerLoadsTypedEntGraph|TestTask4ExternalConsumerGeneratesCRUDTransaction|TestTask5ExternalConsumerDelegatesEntGeneration|TestGenerationPluginExternalConsumer|TestReferenceNexactlInspect' -count=1

source-contracts:
	GOWORK=off GOTOOLCHAIN=local GOENV=off go test ./sourceplugin/... ./plugins/nexactl/source -count=1
	GOWORK=off GOTOOLCHAIN=local GOENV=off go test ./integration -run '^TestSourceExternalConsumer$$' -count=1

service-bundles:
	GOWORK=off GOTOOLCHAIN=local GOENV=off go test ./quality/readmodel ./plugins/service/core ./plugins/service/job ./plugins/service/quality -count=1
	GOWORK=off GOTOOLCHAIN=local GOENV=off go test ./integration -run 'FrameworkMinimum|OfficialSource|SourceBundle(Core|Detach|Optionality)' -count=1 -timeout 30m

runtime:
	GOWORK=off GOTOOLCHAIN=local GOENV=off go test ./runtime/... -count=1
	GOWORK=off GOTOOLCHAIN=local GOENV=off go test ./integration -run 'TestRuntimePackagesExternalConsumers|TestRuntimePackagesOptionalComposition' -count=1

check: test vet build consumer skills adoption-contracts contracts generated-check source-contracts service-bundles runtime
