// Package limits defines shared, dependency-neutral resource bounds.
package limits

// MaxFindingCitedEventIDs bounds the evidence references accepted for one
// upstream finding and consumed from legacy finding payloads during analysis.
const MaxFindingCitedEventIDs = 50
