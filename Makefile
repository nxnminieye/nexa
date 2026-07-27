.PHONY: help smoke test vet build consumer skills adoption-contracts contracts generation-contracts generated-check source-contracts service-contracts source-bundle-runtime service-bundles runtime integration-tests check integration release-check

help:
	@printf '%s\n' \
		'make check         Fast local feedback: compile smoke, vet, build, consumer fixture' \
		'make integration   PR-level checks: make check plus integration tests' \
		'make release-check Release gate: make integration plus the full source-bundle/runtime closure'

smoke:
	GOWORK=off GOTOOLCHAIN=local GOENV=off go test ./... -run '^$$' -count=1 -timeout 10m

test:
	@packages="$$(GOWORK=off go list ./... | awk '$$0 !~ /\/integration$$/')"; \
	GOWORK=off go test $$packages -timeout 30m

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

service-contracts:
	GOWORK=off GOTOOLCHAIN=local GOENV=off go test ./quality/readmodel ./plugins/service/core ./plugins/service/job ./plugins/service/quality -count=1
	GOWORK=off GOTOOLCHAIN=local GOENV=off go test ./integration -run 'FrameworkMinimum|OfficialSource' -count=1 -timeout 30m

source-bundle-runtime:
	GOWORK=off GOTOOLCHAIN=local GOENV=off go test ./integration -run '^TestSourceBundleCore' -count=1 -timeout 30m

service-bundles: service-contracts source-bundle-runtime

runtime:
	GOWORK=off GOTOOLCHAIN=local GOENV=off go test ./runtime/... -count=1
	GOWORK=off GOTOOLCHAIN=local GOENV=off go test ./integration -run 'TestRuntimePackagesExternalConsumers|TestRuntimePackagesOptionalComposition' -count=1

integration-tests:
	GOWORK=off GOTOOLCHAIN=local GOENV=off go test ./integration -skip '^TestSourceBundleCore' -count=1 -timeout 30m

check: smoke vet build consumer

integration: check test integration-tests

release-check: integration source-bundle-runtime
