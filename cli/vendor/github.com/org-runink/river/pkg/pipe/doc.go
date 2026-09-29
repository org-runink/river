// Package pipe is Runink River's replacement for shell pipelines: the plain Go pipeline
// pattern, standard library only.
//
// Two shapes cover what the image's shell scripts did:
//
//   - Command pipelines, the `a | b | c` of a script. [Run] starts every [Command] at once,
//     joins each stdout to the next stdin, and waits for all of them. It fails like
//     `set -o pipefail`: any stage that exits non-zero fails the pipeline, and the error
//     names the stage and carries the tail of its stderr. [Output] does the same and returns
//     the last stage's stdout.
//
//   - Data pipelines, where a script looped over lines or files. A [Source] feeds values
//     into a channel, and [Map], [Filter] and [ParallelMap] are stages that each run in their
//     own goroutine and hand values on through channels. [Collect] and [Drain] end the
//     pipeline. The first error cancels every stage, the way `set -e` stopped a script.
//
// Every stage takes a context, so a cancelled command (Ctrl-C, a timeout) stops the whole
// pipeline, and no goroutine outlives the call that started it.
package pipe
