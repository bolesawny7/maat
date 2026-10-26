// Package invariants holds automated checks for maat's security invariants,
// described in docs/security-model.md. They are tests rather than runtime code so
// CI can require them as a dedicated gate: a change that, say, adds a network
// plug or a shell call fails here.
package invariants
