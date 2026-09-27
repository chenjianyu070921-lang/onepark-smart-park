package server

import (
	"context"
	"encoding/json"
	"strconv"

	"onepark/app/billing-service/internal/logic"
	"onepark/app/billing-service/internal/svc"
	"onepark/app/billing-service/internal/types"
	"onepark/common/ctxdata"
	billingpb "onepark/proto/billing"
	commonpb "onepark/proto/common"

	"google.golang.org/grpc/metadata"
)

// BillingServer 实现 billingpb.BillingServiceServer, 复用现有 HTTP logic,
// 与网关 HTTP 入口共享同一套计费逻辑; 补齐原"仅 Ping"的死契约.
type BillingServer struct {
	svcCtx *svc.ServiceContext
	billingpb.UnimplementedBillingServiceServer
}

// NewBillingServer 构造计费 gRPC server.
func NewBillingServer(svcCtx *svc.ServiceContext) *BillingServer {
	return &BillingServer{svcCtx: svcCtx}
}

// withIdentity 从 gRPC metadata 注入身份上下文(对齐 HTTP IdentityFromHeader);
// 租户缺失时回退到配置 DefaultTenantId, 保证内部 gRPC 调用在缺少租户头时也可用.
func (s *BillingServer) withIdentity(ctx context.Context) context.Context {
	tenantID := s.svcCtx.Config.DefaultTenantId
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if v := firstMD(md, ctxdata.CtxTenantId); v != "" {
			if id, err := strconv.ParseInt(v, 10, 64); err == nil {
				tenantID = id
			}
		}
		if v := firstMD(md, ctxdata.CtxUserId); v != "" {
			if id, err := strconv.ParseInt(v, 10, 64); err == nil {
				ctx = ctxdata.SetUserId(ctx, id)
			}
		}
		if v := firstMD(md, ctxdata.CtxRoleIds); v != "" {
			ctx = ctxdata.SetRoleIds(ctx, v)
		}
	}
	return ctxdata.SetTenantId(ctx, tenantID)
}

func firstMD(md metadata.MD, key string) string {
	if v := md.Get(key); len(v) > 0 {
		return v[0]
	}
	return ""
}

// Ping 存活探针.
func (s *BillingServer) Ping(_ context.Context, _ *commonpb.Empty) (*commonpb.Empty, error) {
	return &commonpb.Empty{}, nil
}

// GetBillRules 计费规则列表(复用 RuleListLogic).
func (s *BillingServer) GetBillRules(ctx context.Context, in *billingpb.GetBillRulesRequest) (*billingpb.GetBillRulesResponse, error) {
	resp, err := logic.NewRuleListLogic(s.withIdentity(ctx), s.svcCtx).
		RuleList(&types.RuleListRequest{ZoneId: in.ZoneId, Status: in.Status})
	if err != nil {
		return nil, err
	}
	items := make([]*billingpb.BillRuleItem, 0, len(resp.List))
	for _, r := range resp.List {
		cfgJSON, _ := json.Marshal(r.Config)
		items = append(items, &billingpb.BillRuleItem{
			Id:        r.Id,
			Name:      r.Name,
			ZoneId:    r.ZoneId,
			RuleType:  r.RuleType,
			Config:    string(cfgJSON),
			Status:    r.Status,
			CreatedAt: r.CreatedAt,
		})
	}
	return &billingpb.GetBillRulesResponse{Total: resp.Total, List: items}, nil
}

// GetBills 账单列表(复用 BillListLogic).
func (s *BillingServer) GetBills(ctx context.Context, in *billingpb.GetBillsRequest) (*billingpb.GetBillsResponse, error) {
	resp, err := logic.NewBillListLogic(s.withIdentity(ctx), s.svcCtx).
		BillList(&types.BillListRequest{ZoneId: in.ZoneId, Status: in.Status, Page: in.Page, PageSize: in.PageSize})
	if err != nil {
		return nil, err
	}
	items := make([]*billingpb.BillItem, 0, len(resp.List))
	for _, b := range resp.List {
		items = append(items, &billingpb.BillItem{
			BillNo:      b.BillNo,
			ZoneId:      b.ZoneId,
			RuleId:      b.RuleId,
			PeriodStart: b.PeriodStart,
			PeriodEnd:   b.PeriodEnd,
			UsageKwh:    b.UsageKwh,
			Amount:      b.Amount,
			Status:      b.Status,
			CreatedAt:   b.CreatedAt,
		})
	}
	return &billingpb.GetBillsResponse{Total: resp.Total, Page: resp.Page, PageSize: resp.PageSize, List: items}, nil
}

// CalculateBill 按区域+账期生成账单(复用 BillGenerateLogic).
func (s *BillingServer) CalculateBill(ctx context.Context, in *billingpb.CalculateBillRequest) (*billingpb.CalculateBillResponse, error) {
	resp, err := logic.NewBillGenerateLogic(s.withIdentity(ctx), s.svcCtx).
		BillGenerate(&types.BillGenerateRequest{ZoneId: in.ZoneId, Period: in.Period, RuleId: in.RuleId})
	if err != nil {
		return nil, err
	}
	details := make([]*billingpb.BillDetailItem, 0, len(resp.Detail))
	for _, d := range resp.Detail {
		details = append(details, &billingpb.BillDetailItem{Name: d.Name, Usage: d.Usage, Price: d.Price, Amount: d.Amount})
	}
	return &billingpb.CalculateBillResponse{
		BillNo:      resp.BillNo,
		ZoneId:      resp.ZoneId,
		PeriodStart: resp.PeriodStart,
		PeriodEnd:   resp.PeriodEnd,
		RuleId:      resp.RuleId,
		RuleName:    resp.RuleName,
		UsageKwh:    resp.UsageKwh,
		Amount:      resp.Amount,
		Detail:      details,
	}, nil
}
