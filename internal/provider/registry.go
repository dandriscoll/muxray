package provider

// adapters is the ordered set of provider adapters. Order is deterministic and
// is the tie-breaker when two providers match with equal confidence (earlier
// wins, though distinct signatures make ties rare).
var adapters = []adapter{
	claudeAdapter(),
	codexAdapter(),
	copilotAdapter(),
}
