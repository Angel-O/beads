package main

import (
	"context"
)

func runHistoryProxiedServer(ctx context.Context, issueID string, limit int, showEvents bool) error {
	uw, err := openProxiedListUOW(ctx)
	if err != nil {
		return HandleError("%v", err)
	}
	defer uw.Close(ctx)

	return runHistory(ctx, uw.IssueUseCase(), issueID, limit, showEvents)
}

func runBulkHistoryProxiedServer(ctx context.Context, issueIDs []string, limit int) error {
	uw, err := openProxiedListUOW(ctx)
	if err != nil {
		return HandleErrorRespectJSON("%v", err)
	}
	defer uw.Close(ctx)

	return runBulkHistory(ctx, uw.IssueUseCase(), issueIDs, limit)
}
