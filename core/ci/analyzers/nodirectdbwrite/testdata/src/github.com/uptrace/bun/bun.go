// Package bun is a minimal stub of github.com/uptrace/bun providing the
// types the nodirectdbwrite analyzer reasons about: DB and Tx, plus the
// banned write builders. The stub lets analysistest exercise the
// analyzer without vendoring the real bun package into testdata.
package bun

type DB struct{}

type Tx struct{}

type InsertQuery struct{}
type UpdateQuery struct{}
type DeleteQuery struct{}
type MergeQuery struct{}
type RawQuery struct{}
type TruncateQuery struct{}

func (db *DB) NewInsert() *InsertQuery     { return &InsertQuery{} }
func (db *DB) NewUpdate() *UpdateQuery     { return &UpdateQuery{} }
func (db *DB) NewDelete() *DeleteQuery     { return &DeleteQuery{} }
func (db *DB) NewMerge() *MergeQuery       { return &MergeQuery{} }
func (db *DB) NewRaw(_ string) *RawQuery   { return &RawQuery{} }
func (db *DB) NewTruncate() *TruncateQuery { return &TruncateQuery{} }

func (tx *Tx) NewInsert() *InsertQuery     { return &InsertQuery{} }
func (tx *Tx) NewUpdate() *UpdateQuery     { return &UpdateQuery{} }
func (tx *Tx) NewDelete() *DeleteQuery     { return &DeleteQuery{} }
func (tx *Tx) NewMerge() *MergeQuery       { return &MergeQuery{} }
func (tx *Tx) NewRaw(_ string) *RawQuery   { return &RawQuery{} }
func (tx *Tx) NewTruncate() *TruncateQuery { return &TruncateQuery{} }
