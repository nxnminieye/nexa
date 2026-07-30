// @nexa $contract: "nexa.dev/source-comment/v1"
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// @nexa label.zh-CN: "任务运行"
// @nexa label.en-US: "Job run"
// @nexa description.zh-CN: "任务运行"
// @nexa description.en-US: "Job run"
// @nexa scope: "global"
type Run struct{ ent.Schema }

func (Run) Config() ent.Config { return ent.Config{Table: "job_runs"} }

func (Run) Fields() []ent.Field {
	return []ent.Field{
		// @nexa label.zh-CN: "运行标识"
		// @nexa label.en-US: "Run ID"
		// @nexa description.zh-CN: "运行标识"
		// @nexa description.en-US: "Run ID"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		field.String("run_id").Unique().Immutable(),
		// @nexa label.zh-CN: "计划标识"
		// @nexa label.en-US: "Schedule ID"
		// @nexa description.zh-CN: "计划标识"
		// @nexa description.en-US: "Schedule ID"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		field.String("schedule_id").Optional().Immutable(),
		// @nexa label.zh-CN: "任务标识"
		// @nexa label.en-US: "Task ID"
		// @nexa description.zh-CN: "任务标识"
		// @nexa description.en-US: "Task ID"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		field.String("task_id").Immutable(),
		// @nexa label.zh-CN: "运行状态"
		// @nexa label.en-US: "Run status"
		// @nexa description.zh-CN: "运行状态"
		// @nexa description.en-US: "Run status"
		// @nexa ui.control: "select"
		// @nexa visibility: "public"
		field.Enum("status").Values("running", "succeeded", "failed", "canceled").Default("running"),
		// @nexa label.zh-CN: "运行载荷"
		// @nexa label.en-US: "Run payload"
		// @nexa description.zh-CN: "运行载荷"
		// @nexa description.en-US: "Run payload"
		// @nexa ui.control: "sensitive"
		// @nexa visibility: "sensitive"
		field.Bytes("payload").Optional().Sensitive(),
		// @nexa label.zh-CN: "运行输出"
		// @nexa label.en-US: "Run output"
		// @nexa description.zh-CN: "运行输出"
		// @nexa description.en-US: "Run output"
		// @nexa ui.control: "sensitive"
		// @nexa visibility: "sensitive"
		field.Bytes("output").Optional().Sensitive(),
		// @nexa label.zh-CN: "错误码"
		// @nexa label.en-US: "Error code"
		// @nexa description.zh-CN: "错误码"
		// @nexa description.en-US: "Error code"
		// @nexa ui.control: "readonly"
		// @nexa visibility: "public"
		field.String("error_code").Optional(),
		// @nexa label.zh-CN: "开始时间"
		// @nexa label.en-US: "Started at"
		// @nexa description.zh-CN: "开始时间"
		// @nexa description.en-US: "Started at"
		// @nexa ui.control: "datetime"
		// @nexa visibility: "public"
		field.Time("started_at").Immutable(),
		// @nexa label.zh-CN: "完成时间"
		// @nexa label.en-US: "Completed at"
		// @nexa description.zh-CN: "完成时间"
		// @nexa description.en-US: "Completed at"
		// @nexa ui.control: "datetime"
		// @nexa visibility: "public"
		field.Time("completed_at").Optional().Nillable(),
	}
}
