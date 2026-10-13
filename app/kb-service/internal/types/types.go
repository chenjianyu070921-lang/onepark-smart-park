// Package types 与 kb.api 保持一致(手写维护; 契约以 kb.api 为准).
package types

// ---------- 文档 ----------

type CreateDocumentRequest struct {
	Title    string `json:"title"`
	Source   string `json:"source,optional"`
	Category string `json:"category,optional"`
	Content  string `json:"content"`
}

type CreateDocumentResponse struct {
	Id         int64 `json:"id"`
	ChunkCount int   `json:"chunkCount"`
}

type ListDocumentsRequest struct {
	Category string `form:"category,optional"`
	Page     int    `form:"page,default=1"`
	PageSize int    `form:"pageSize,default=20"`
}

type ListDocumentsResponse struct {
	Total int64          `json:"total"`
	List  []DocumentItem `json:"list"`
}

type DocumentItem struct {
	Id         int64  `json:"id"`
	Title      string `json:"title"`
	Source     string `json:"source"`
	Category   string `json:"category"`
	ChunkCount int    `json:"chunkCount"`
	Status     int    `json:"status"`
	CreatedAt  string `json:"createdAt"`
}

type DeleteDocumentRequest struct {
	Id int64 `path:"id"`
}

type DeleteDocumentResponse struct {
	Deleted int64 `json:"deleted"`
}

// ---------- 问答 ----------

type AskRequest struct {
	Question string `json:"question"`
	TopK     int    `json:"topK,optional"`
}

type AskResponse struct {
	Answer    string     `json:"answer"`
	Mode      string     `json:"mode"` // extractive / llm / no_hit
	QaId      int64      `json:"qaId"`
	Citations []Citation `json:"citations"`
	TopScore  float64    `json:"topScore"`
	ElapsedMs int64      `json:"elapsedMs"`
}

type Citation struct {
	ChunkId    int64   `json:"chunkId"`
	DocumentId int64   `json:"documentId"`
	Title      string  `json:"title"`
	Source     string  `json:"source"`
	Seq        int     `json:"seq"`
	Score      float64 `json:"score"`
	Snippet    string  `json:"snippet"`
}

// ---------- 日志与反馈 ----------

type ListQaLogsRequest struct {
	Page     int `form:"page,default=1"`
	PageSize int `form:"pageSize,default=20"`
}

type ListQaLogsResponse struct {
	Total int64   `json:"total"`
	List  []QaLog `json:"list"`
}

type QaLog struct {
	Id        int64   `json:"id"`
	Question  string  `json:"question"`
	Answer    string  `json:"answer"`
	Mode      string  `json:"mode"`
	TopScore  float64 `json:"topScore"`
	ElapsedMs int64   `json:"elapsedMs"`
	CreatedAt string  `json:"createdAt"`
}

type FeedbackRequest struct {
	QaId    int64  `json:"qaId"`
	Vote    int    `json:"vote"`
	Comment string `json:"comment,optional"`
}

type FeedbackResponse struct {
	Ok bool `json:"ok"`
}
