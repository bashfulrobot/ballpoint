// Package dispatch drains the walk's outward queue and runs one claude
// headless assessment job per task, writing each assessment back through the
// sanctioned work-log writer. Splitting assessment out of the walk means a
// long job runs behind the walk rather than blocking it. Issue #6.
//
// The core is pure and testable: prompt construction, result parsing, argv
// building, and orchestration take the two shell-outs (claude, the td scripts)
// and the clock as injected functions. The CLI layer supplies the real exec
// functions.
package dispatch
