package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/knadh/listmonk/internal/subimporter"
	"github.com/knadh/listmonk/models"
	"github.com/labstack/echo"
	"github.com/lib/pq"
)

type bouncesWrap struct {
	Results []models.Bounce `json:"results"`

	Total   int `json:"total"`
	PerPage int `json:"per_page"`
	Page    int `json:"page"`
}

// handleGetBounces handles retrieval of bounce records.
func handleGetBounces(c echo.Context) error {
	var (
		app = c.Get("app").(*App)
		pg  = getPagination(c.QueryParams(), 20, 50)
		out bouncesWrap

		id, _     = strconv.Atoi(c.Param("id"))
		campID, _ = strconv.Atoi(c.Param("campaign_id"))
		source    = c.FormValue("source")
		orderBy   = c.FormValue("order_by")
		order     = c.FormValue("order")
	)

	// Fetch one list.
	single := false
	if id > 0 {
		single = true
	}

	// Sort params.
	if !strSliceContains(orderBy, bounceQuerySortFields) {
		orderBy = "created_at"
	}
	if order != sortAsc && order != sortDesc {
		order = sortDesc
	}

	stmt := fmt.Sprintf(app.queries.QueryBounces, orderBy, order)

	if err := db.Select(&out.Results, stmt, id, campID, source, pg.Offset, pg.Limit); err != nil {
		app.log.Printf("error fetching bounces: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError,
			app.i18n.Ts("globals.messages.errorFetching",
				"name", "{globals.terms.bounce}", "error", pqErrMsg(err)))
	}
	if len(out.Results) == 0 {
		out.Results = []models.Bounce{}
		return c.JSON(http.StatusOK, okResp{out})
	}

	if single {
		return c.JSON(http.StatusOK, okResp{out.Results[0]})
	}

	// Meta.
	out.Total = out.Results[0].Total
	out.Page = pg.Page
	out.PerPage = pg.PerPage

	return c.JSON(http.StatusOK, okResp{out})
}

// handleDeleteBounces handles bounce deletion, either a single one (ID in the URI), or a list.
func handleDeleteBounces(c echo.Context) error {
	var (
		app    = c.Get("app").(*App)
		pID    = c.Param("id")
		all, _ = strconv.ParseBool(c.QueryParam("all"))
		IDs    = pq.Int64Array{}
	)

	// Is it an /:id call?
	if pID != "" {
		id, _ := strconv.ParseInt(pID, 10, 64)
		if id < 1 {
			return echo.NewHTTPError(http.StatusBadRequest, app.i18n.T("globals.messages.invalidID"))
		}
		IDs = append(IDs, id)
	} else if !all {
		// Multiple IDs.
		i, err := parseStringIDs(c.Request().URL.Query()["id"])
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest,
				app.i18n.Ts("globals.messages.invalidID", "error", err.Error()))
		}

		if len(i) == 0 {
			return echo.NewHTTPError(http.StatusBadRequest,
				app.i18n.Ts("globals.messages.invalidID"))
		}
		IDs = i
	}

	if _, err := app.queries.DeleteBounces.Exec(IDs); err != nil {
		app.log.Printf("error deleting bounces: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError,
			app.i18n.Ts("globals.messages.errorDeleting",
				"name", "{globals.terms.bounce}", "error", pqErrMsg(err)))
	}

	return c.JSON(http.StatusOK, okResp{true})
}

// handleBounceWebhook renders the HTML preview of a template.
func handleBounceWebhook(c echo.Context) error {
	var (
		app     = c.Get("app").(*App)
		service = c.Param("id")

		b models.Bounce
	)

	switch service {
	// Native postback.
	case "":
		if err := c.Bind(&b); err != nil {
			return err
		}

		if err := validateBounceFields(b, app); err != nil {
			return err
		}

		b.Email = strings.ToLower(b.Email)

		if len(b.Meta) == 0 {
			b.Meta = json.RawMessage("{}")
		}

		if b.CreatedAt.Year() == 0 {
			b.CreatedAt = time.Now()
		}

	// Amazon SES.
	case "ses":

	// SendGrid.
	case "sendgrid":
	default:
		return echo.NewHTTPError(http.StatusBadRequest, app.i18n.Ts("bounces.unknownService"))
	}

	// Record the bounce.
	if err := app.bounce.Record(b); err != nil {
		app.log.Printf("error recording bounce: %v", err)
	}

	return c.JSON(http.StatusOK, okResp{true})
}

func validateBounceFields(b models.Bounce, app *App) error {
	if b.Email == "" && b.SubscriberUUID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, app.i18n.T("globals.messages.invalidData"))
	}

	if b.Email != "" && !subimporter.IsEmail(b.Email) {
		return echo.NewHTTPError(http.StatusBadRequest, app.i18n.T("globals.messages.invalidEmail"))
	}

	if b.SubscriberUUID != "" && !reUUID.MatchString(b.SubscriberUUID) {
		return echo.NewHTTPError(http.StatusBadRequest, app.i18n.T("globals.messages.invalidUUID"))
	}

	return nil
}
