// Package event implements the typed-event log that backs the v1.0
// pcke "durable code memory" model.
//
// Where the legacy schema (v0.9 and earlier) used flat key prefixes
// (kn: knowledge nodes, rel: relations, el: evolution log, nt: notes),
// this package introduces a single append-only event log under five
// typed prefixes — Entity, Decision, Observation, Outcome, Link — plus
// a paired reverse-edge index (lr:) that makes "what depends on this"
// queries O(out-degree) instead of O(all-edges).
//
// Every fact is immutable. Updates append a new version with a
// Supersedes pointer back to the prior version's key. Time-travel
// reads (AsOf) and cross-event chain walks (ResolveSupersedes) are
// first-class.
//
// # Schema at a glance
//
//	e:<id>:v<N>     entity        (file, function, type, module)
//	d:<id>:v<N>     decision      (typed assertion: must/should/may + scope)
//	l:<linkID>:v<N> link          (first-class edge, src+edge+dst composite)
//	lr:<dst>:<edge>:<src>        reverse-edge index (NOT versioned;
//	                              value is the latest forward-link key)
//	o:<id>:v<N>     observation   (Phase 14: agent/scanner action)
//	x:<id>:v<N>     outcome       (Phase 14: derived event)
//
// Version numbers are zero-padded to 16 decimal digits so lexicographic
// order over keys equals numeric order over versions. A single B+tree
// cursor seek positions at the start of an id's chain; the highest key
// in the chain is the current version.
//
// # API surface
//
//	enc, _ := event.Encode(myEvent)            // []byte ready for kdb.Put
//	evt, _ := event.Decode(enc, "logical-id")  // sum-typed assertion to *Entity / *Decision / etc.
//
//	store := event.New(db)
//	key, _ := store.Append(ctx, &event.Entity{EID: "internal/kdb/db.go", Type: "file"})
//	latest, _ := store.Latest(ctx, event.KindEntity, "internal/kdb/db.go")
//	_ = store.History(ctx, event.KindEntity, "internal/kdb/db.go", func(e event.Event) error { ... })
//	_ = store.IterateKind(ctx, event.KindDecision, func(e event.Event) error { ... })
//	atT, _ := store.AsOf(ctx, event.KindEntity, "internal/kdb/db.go", t)
//	chain, _ := store.ResolveSupersedes(ctx, key, 32)
//
//	store.AppendLink(ctx, &event.Link{
//	    SrcRef: "e:internal/kdb/db.go", EdgeType: "imports", DstRef: "e:internal/kdb/btree",
//	})
//	_ = store.ReverseLinks(ctx, "e:internal/kdb/btree", "imports", func(l *event.Link) error { ... })
//
// # Lifecycles, severities, scopes
//
// Lifecycle marks whether an event is currently in force, has been
// superseded by a later version, or is historical archive. Severity
// (must / should / may) and Scope (file / module / global) decorate
// Decision events. All three are wire-stable enums: do not renumber
// existing values when extending them.
//
// # Forward compatibility
//
// Event records use the kdb encoding schema v1 tagged-record format.
// Unknown payload field IDs are decoded into a generic bucket and
// dropped, so future writers may add fields without breaking older
// readers. The schema-version byte at the head of each value is the
// hard fence: bump only when adding fields older readers must reject.
//
// # Contract for migrations 0011–0014 (F12.T6)
//
// The data-translation migrations introduced by F12.T6 will translate
// legacy kn:/rel:/nt:/el: records into typed events. Those migrations
// must run inside an existing kdb WriteTx (so all writes commit or
// none do), and they need a way to call into this package without
// nesting a fresh db.Update. The exported guarantees they rely on:
//
//   - event.Store.appendInTx(wtx, e) writes the next version of e
//     using the supplied transaction. It is package-internal; the
//     migrations live in this module and call it directly.
//   - event.BuildKey and event.BuildReverseLinkKey are stable. Migrations
//     that need to craft deterministic keys (e.g. mapping kn:foo.go to
//     e:foo.go:v0000000000000001) build them with these helpers so the
//     escape rules stay consistent.
//   - event.Encode round-trips with event.Decode for every concrete
//     event type. Migrations that need to round-trip a synthesized
//     header may use Encode directly; otherwise prefer Append/AppendLink.
//   - The schema-version field on the meta page is bumped to 10 by
//     migration 0010 (a pure marker). Migrations 0011–0014 may assume
//     the event prefixes are reserved.
//
// Migrations 0011–0014 are not part of T1; this package only ships
// the primitives they will consume.
package event
