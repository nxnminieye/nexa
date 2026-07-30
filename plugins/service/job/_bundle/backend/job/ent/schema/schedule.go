// @nexa $contract: "nexa.dev/source-comment/v1"
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// @nexa label.zh-CN: "任务计划"
// @nexa label.en-US: "Job schedule"
// @nexa description.zh-CN: "任务计划"
// @nexa description.en-US: "Job schedule"
// @nexa scope: "global"
type Schedule struct{ ent.Schema }

func (Schedule) Config() ent.Config { return ent.Config{Table: "job_schedules"} }

func (Schedule) Fields() []ent.Field {
	return []ent.Field{
		// @nexa label.zh-CN: "计划标识"
		// @nexa label.en-US: "Schedule key"
		// @nexa description.zh-CN: "计划标识"
		// @nexa description.en-US: "Schedule key"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		field.String("key").Unique().Immutable(),
		// @nexa label.zh-CN: "Cron 表达式"
		// @nexa label.en-US: "Cron expression"
		// @nexa description.zh-CN: "Cron 表达式"
		// @nexa description.en-US: "Cron expression"
		// @nexa ui.control: "text"
		// @nexa visibility: "public"
		field.String("cron"),
		// @nexa label.zh-CN: "任务标识"
		// @nexa label.en-US: "Task ID"
		// @nexa description.zh-CN: "任务标识"
		// @nexa description.en-US: "Task ID"
		// @nexa ui.control: "reference"
		// @nexa visibility: "public"
		field.String("task_id"),
		// @nexa label.zh-CN: "任务载荷"
		// @nexa label.en-US: "Task payload"
		// @nexa description.zh-CN: "任务载荷"
		// @nexa description.en-US: "Task payload"
		// @nexa visibility: "public"
		field.Bytes("payload").Optional(),
		// @nexa label.zh-CN: "启用"
		// @nexa label.en-US: "Enabled"
		// @nexa description.zh-CN: "启用"
		// @nexa description.en-US: "Enabled"
		// @nexa ui.control: "switch"
		// @nexa visibility: "public"
		field.Bool("enabled").Default(true),
	}
}
