package repository

import (
	"github.com/hypoballad/virgil/internal/db"
)

type Repository struct {
	Sessions     *SessionRepository
	Turns        *TurnRepository
	ToolCalls    *ToolCallRepository
	LLMExchanges *LLMExchangeRepository // 追加
	Symbols      *SymbolRepository      // 追加
	Calls        *CallRepository
	Imports      *ImportRepository

	db *db.DB
}

func New(database *db.DB) *Repository {
	return &Repository{
		Sessions:     NewSessionRepository(database),
		Turns:        NewTurnRepository(database),
		ToolCalls:    NewToolCallRepository(database),
		LLMExchanges: NewLLMExchangeRepository(database), // 追加
		Symbols:      NewSymbolRepository(database),      // 追加
		Calls:        NewCallRepository(database),
		Imports:      NewImportRepository(database),
		db:           database,
	}
}
