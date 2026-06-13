package user

import (
	"context"

	"github.com/maomeng/aim/app/user-service/internal/metrics"
	"github.com/maomeng/aim/app/user-service/internal/repo"
	userpb "github.com/maomeng/aim/app/user-service/pb/user"
	"github.com/maomeng/aim/pkg/errors"
)

func (l *Logic) Recharge(ctx context.Context, req *userpb.RechargeReq) (*userpb.RechargeResp, error) {
	logger := l.ctxLogger(ctx)

	if req.Amount <= 0 {
		err := errors.New(errors.CodeInvalidParam, "amount must be > 0")
		logger.Errorf("method=Recharge user_id=%d error=%v", req.UserId, err)
		return nil, err
	}

	newBalance, err := l.userRepo.UpdateBalance(ctx, req.UserId, req.Amount)
	if err != nil {
		logger.Errorf("method=Recharge user_id=%d amount=%f error=%v", req.UserId, req.Amount, err)
		return nil, errors.Wrap(errors.CodeInternal, "recharge failed", err)
	}

	logger.Infof("method=Recharge user_id=%d amount=%f new_balance=%f", req.UserId, req.Amount, newBalance)
	metrics.RechargeAmountTotal.Add(float64(req.Amount), "cny")
	metrics.RechargeCountTotal.Inc("alipay")
	return &userpb.RechargeResp{NewBalance: newBalance}, nil
}

func (l *Logic) DeductBalance(ctx context.Context, req *userpb.DeductBalanceReq) (*userpb.DeductBalanceResp, error) {
	logger := l.ctxLogger(ctx)

	if req.Amount <= 0 {
		err := errors.New(errors.CodeInvalidParam, "amount must be > 0")
		logger.Errorf("method=DeductBalance user_id=%d error=%v", req.UserId, err)
		return nil, err
	}

	newBalance, err := l.userRepo.UpdateBalance(ctx, req.UserId, -req.Amount)
	if err != nil {
		if err == repo.ErrInsufficientBalance {
			logger.Errorf("method=DeductBalance user_id=%d error=insufficient_balance", req.UserId)
			return nil, errors.New(errors.CodeForbidden, "余额不足，请充值")
		}
		logger.Errorf("method=DeductBalance user_id=%d amount=%f error=%v", req.UserId, req.Amount, err)
		return nil, errors.Wrap(errors.CodeInternal, "deduct balance failed", err)
	}

	logger.Infof("method=DeductBalance user_id=%d amount=%f new_balance=%f", req.UserId, req.Amount, newBalance)
	metrics.ConsumptionAmountTotal.Add(float64(req.Amount), "model_call")
	metrics.ConsumptionCountTotal.Inc("model_call")
	return &userpb.DeductBalanceResp{NewBalance: newBalance}, nil
}

func (l *Logic) GetBalance(ctx context.Context, req *userpb.GetBalanceReq) (*userpb.GetBalanceResp, error) {
	logger := l.ctxLogger(ctx)

	balance, err := l.userRepo.GetBalance(ctx, req.UserId)
	if err != nil {
		logger.Errorf("method=GetBalance user_id=%d error=%v", req.UserId, err)
		return nil, errors.Wrap(errors.CodeInternal, "get balance failed", err)
	}

	return &userpb.GetBalanceResp{Balance: balance}, nil
}
