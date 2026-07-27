// Package directwrite applies a fully prevalidated set of generated-source
// mutations directly to a consumer repository.
//
// The package deliberately has no Git, staging, rollback, or ownership-manifest
// behavior. Callers use the returned report and the repository Git diff as the
// recovery and review boundary.
package directwrite
