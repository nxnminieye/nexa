package schema

import (
	"entgo.io/ent"
	entschema "entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"github.com/nxnminieye/nexa/nexaent"
)

type Run struct{ ent.Schema }

func (Run) Config() ent.Config { return ent.Config{Table: "job_runs"} }

func (Run) Annotations() []entschema.Annotation {
	return []entschema.Annotation{nexaent.Schema(schemaMeta("run", "任务运行", "Job run"))}
}

func (Run) Fields() []ent.Field {
	return []ent.Field{
		field.String("run_id").Unique().Immutable().Annotations(fieldMeta("run.run_id", "运行标识", "Run ID", nexaent.UIHintReadonly, nexaent.VisibilityPublic)),
		field.String("schedule_id").Optional().Immutable().Annotations(fieldMeta("run.schedule_id", "计划标识", "Schedule ID", nexaent.UIHintReadonly, nexaent.VisibilityPublic)),
		field.String("task_id").Immutable().Annotations(fieldMeta("run.task_id", "任务标识", "Task ID", nexaent.UIHintReadonly, nexaent.VisibilityPublic)),
		field.Enum("status").Values("running", "succeeded", "failed", "canceled").Default("running").Annotations(fieldMeta("run.status", "运行状态", "Run status", nexaent.UIHintSelect, nexaent.VisibilityPublic)),
		field.Bytes("payload").Optional().Sensitive().Annotations(fieldMeta("run.payload", "运行载荷", "Run payload", nexaent.UIHintSensitive, nexaent.VisibilitySensitive)),
		field.Bytes("output").Optional().Sensitive().Annotations(fieldMeta("run.output", "运行输出", "Run output", nexaent.UIHintSensitive, nexaent.VisibilitySensitive)),
		field.String("error_code").Optional().Annotations(fieldMeta("run.error_code", "错误码", "Error code", nexaent.UIHintReadonly, nexaent.VisibilityPublic)),
		field.Time("started_at").Immutable().Annotations(fieldMeta("run.started_at", "开始时间", "Started at", nexaent.UIHintDatetime, nexaent.VisibilityPublic)),
		field.Time("completed_at").Optional().Nillable().Annotations(fieldMeta("run.completed_at", "完成时间", "Completed at", nexaent.UIHintDatetime, nexaent.VisibilityPublic)),
	}
}
