// Package ecode 定义 M7 企业知识库模块的错误码.
// 格式: {Module}-{Level}-{Code}, 见 common/errorx/codes.go
package ecode

const (
	// 3001~3009 文档接入
	// ErrDocumentCreateFailed 文档入库失败(含切片事务)
	ErrDocumentCreateFailed = "M7-E-3001"
	// ErrDocumentNotFound 文档不存在(或不属于当前租户)
	ErrDocumentNotFound = "M7-E-3002"
	// ErrDocumentContentEmpty 正文为空
	ErrDocumentContentEmpty = "M7-E-3003"
	// ErrDocumentTitleEmpty 标题为空
	ErrDocumentTitleEmpty = "M7-E-3004"
	// ErrDocumentDeleteFailed 删除失败
	ErrDocumentDeleteFailed = "M7-E-3005"

	// 3901~3909 检索与问答
	// ErrRetrieveFailed 检索失败
	ErrRetrieveFailed = "M7-E-3901"
	// ErrQuestionEmpty 问题为空
	ErrQuestionEmpty = "M7-E-3902"
	// ErrQaLogFailed 问答日志写入失败
	ErrQaLogFailed = "M7-E-3903"
	// ErrFeedbackFailed 反馈写入失败
	ErrFeedbackFailed = "M7-E-3904"
	// ErrQaNotFound 问答记录不存在
	ErrQaNotFound = "M7-E-3905"
)
