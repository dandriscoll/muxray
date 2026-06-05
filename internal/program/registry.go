package program

// adapters is the ordered set of program adapters. Order is deterministic and
// is the tie-breaker when two programs match with equal confidence (earlier
// wins, though distinct signatures make ties rare).
var adapters = []adapter{
	claudeAdapter(),
	codexAdapter(),
	copilotAdapter(),
}
