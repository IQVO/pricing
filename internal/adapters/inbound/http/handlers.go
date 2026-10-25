package http

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/IQVO/pricing/internal/application/ports"
	"github.com/IQVO/pricing/internal/application/usecases"
)

// maxIDLength bounds a path identifier; a longer one cannot name a resource.
const maxIDLength = 128

// pathID returns the named path parameter, or false after writing 404 for one
// that can never name a resource.
func pathID(w http.ResponseWriter, r *http.Request, name string) (string, bool) {
	id := chi.URLParam(r, name)
	if id == "" || len(id) > maxIDLength {
		writeError(w, r, ports.ErrNotFound)
		return "", false
	}
	return id, true
}

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, statusBody{Status: "ok"})
}

func (s *Server) handleCreateItemPrice(w http.ResponseWriter, r *http.Request) {
	var req createItemPriceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	v, err := s.CreateItemPrice.Execute(r.Context(), usecases.CreateItemPriceInput{CatalogItemID: req.CatalogItemID, ListPrice: req.ListPrice.money()})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, itemPriceOut(v))
}

func (s *Server) handleGetItemPrice(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "catalogItemId")
	if !ok {
		return
	}
	v, err := s.GetItemPrice.Execute(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, itemPriceOut(v))
}

func (s *Server) handleListItemPrices(w http.ResponseWriter, r *http.Request) {
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			writeProblem(w, r, unprocessable("validation-failed", "Validation failed"), "limit must be an integer")
			return
		}
		limit = n
	}
	v, err := s.ListItemPrices.Execute(r.Context(), r.URL.Query().Get("after"), limit)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, itemPriceListOut(v))
}

func (s *Server) handleSetListPrice(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "catalogItemId")
	if !ok {
		return
	}
	var req setListPriceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	v, err := s.SetListPrice.Execute(r.Context(), usecases.SetListPriceInput{CatalogItemID: id, ListPrice: req.ListPrice.money(), ExpectedVersion: req.Version})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, itemPriceOut(v))
}

func (s *Server) handleWithdraw(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "catalogItemId")
	if !ok {
		return
	}
	v, err := s.WithdrawPrice.Execute(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, itemPriceOut(v))
}

func (s *Server) handleScheduleSale(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "catalogItemId")
	if !ok {
		return
	}
	var req scheduleSaleRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	v, err := s.ScheduleSale.Execute(r.Context(), usecases.ScheduleSaleInput{CatalogItemID: id, Price: req.Price.money(), From: req.From, To: req.To})
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, saleOut(v))
}

func (s *Server) handleCancelSale(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "catalogItemId")
	if !ok {
		return
	}
	saleID, ok := pathID(w, r, "saleId")
	if !ok {
		return
	}
	v, err := s.CancelSale.Execute(r.Context(), id, saleID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, itemPriceOut(v))
}

func (s *Server) handlePublishSchedule(w http.ResponseWriter, r *http.Request) {
	var req publishScheduleRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	v, err := s.PublishChargeSchedule.Execute(r.Context(), req.input())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, scheduleOut(v))
}

func (s *Server) handleGetSchedule(w http.ResponseWriter, r *http.Request) {
	v, err := s.GetChargeSchedule.Execute(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, scheduleOut(v))
}

func (s *Server) handlePublishTax(w http.ResponseWriter, r *http.Request) {
	var req publishTaxRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	v, err := s.PublishTaxTable.Execute(r.Context(), req.input())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, taxTableOut(v))
}

func (s *Server) handleGetTax(w http.ResponseWriter, r *http.Request) {
	v, err := s.GetTaxTable.Execute(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, taxTableOut(v))
}

func (s *Server) handleIssueQuote(w http.ResponseWriter, r *http.Request) {
	var req issueQuoteRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	v, err := s.IssuePriceQuote.Execute(r.Context(), req.input())
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, quoteOut(v))
}

func (s *Server) handleGetQuote(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "quoteId")
	if !ok {
		return
	}
	v, err := s.GetPriceQuote.Execute(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, quoteOut(v))
}
