package types

import "mime/multipart"

// UploadWorkOrderAttachmentReq 工单附件上传请求(multipart/form-data).
type UploadWorkOrderAttachmentReq struct {
	Id   int64                `path:"id"`   // 工单ID(路径参数)
	File multipart.FileHeader `form:"file"` // 上传文件(表单字段 file)
}

// UploadWorkOrderAttachmentResp 工单附件上传响应.
type UploadWorkOrderAttachmentResp struct {
	AttachmentId int64  `json:"attachment_id"` // 附件记录ID
	ObjectKey    string `json:"object_key"`    // MinIO 对象Key
	FileName     string `json:"file_name"`     // 原始文件名
}

// ListWorkOrderAttachmentsReq 工单附件列表请求.
type ListWorkOrderAttachmentsReq struct {
	Id int64 `path:"id"` // 工单ID(路径参数)
}

// WorkOrderAttachmentItem 工单附件明细项.
type WorkOrderAttachmentItem struct {
	Id        int64  `json:"id"`         // 附件记录ID
	ObjectKey string `json:"object_key"` // MinIO 对象Key
	FileName  string `json:"file_name"`  // 原始文件名
	CreatedAt int64  `json:"created_at"` // 上传时间(秒级时间戳)
}

// WorkOrderAttachmentListResp 工单附件列表响应.
type WorkOrderAttachmentListResp struct {
	List []WorkOrderAttachmentItem `json:"list"`
}
