package schema

import (
	"entgo.io/ent"
	entschema "entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"github.com/nxnminieye/nexa/nexaent"
)

type Schedule struct{ ent.Schema }

func (Schedule) Config() ent.Config { return ent.Config{Table: "job_schedules"} }

func (Schedule) Annotations() []entschema.Annotation {
	return []entschema.Annotation{nexaent.Schema(schemaMeta("schedule", "任务计划", "Job schedule"))}
}

func (Schedule) Fields() []ent.Field {
	return []ent.Field{
		field.String("key").Unique().Immutable().Annotations(fieldMeta("schedule.key", "计划标识", "Schedule key", nexaent.UIHintReadonly, nexaent.VisibilityPublic)),
		field.String("cron").Annotations(fieldMeta("schedule.cron", "Cron 表达式", "Cron expression", nexaent.UIHintText, nexaent.VisibilityPublic)),
		field.String("task_id").Annotations(fieldMeta("schedule.task_id", "任务标识", "Task ID", nexaent.UIHintReference, nexaent.VisibilityPublic)),
		field.Bytes("payload").Optional().Annotations(fieldMeta("schedule.payload", "任务载荷", "Task payload", nexaent.UIHintJSON, nexaent.VisibilityPublic)),
		field.Bool("enabled").Default(true).Annotations(fieldMeta("schedule.enabled", "启用", "Enabled", nexaent.UIHintSwitch, nexaent.VisibilityPublic)),
	}
}
