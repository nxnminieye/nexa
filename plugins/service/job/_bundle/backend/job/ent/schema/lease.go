package schema

import (
	"entgo.io/ent"
	entschema "entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"github.com/nxnminieye/nexa/nexaent"
)

type Lease struct{ ent.Schema }

func (Lease) Config() ent.Config { return ent.Config{Table: "job_leases"} }

func (Lease) Annotations() []entschema.Annotation {
	return []entschema.Annotation{nexaent.Schema(schemaMeta("lease", "任务租约", "Job lease"))}
}

func (Lease) Fields() []ent.Field {
	return []ent.Field{
		field.String("key").Unique().Immutable().Annotations(fieldMeta("lease.key", "租约标识", "Lease key", nexaent.UIHintReadonly, nexaent.VisibilityInternal)),
		field.String("run_id").Immutable().Annotations(fieldMeta("lease.run_id", "运行标识", "Run ID", nexaent.UIHintReadonly, nexaent.VisibilityInternal)),
		field.Time("expires_at").Annotations(fieldMeta("lease.expires_at", "过期时间", "Expires at", nexaent.UIHintDatetime, nexaent.VisibilityInternal)),
	}
}
