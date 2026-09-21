package store

import (
	"context"
	"database/sql"
	"errors"
)

// CreateSurvey 登记一次放流后回捕调查（可在放流数月后进行）。
func (s *Store) CreateSurvey(ctx context.Context, in SurveyInput) (*Survey, error) {
	if in.SurveyDate == "" {
		return nil, rulef("survey_date 不能为空")
	}
	date, err := parseTime(in.SurveyDate)
	if err != nil {
		return nil, err
	}
	if in.Waterbody == "" {
		return nil, rulef("waterbody（调查水体）不能为空")
	}
	sv := &Survey{
		SurveyID:        newID("SV"),
		SurveyRef:       in.SurveyRef,
		SurveyDate:      date,
		Waterbody:       in.Waterbody,
		SiteName:        in.SiteName,
		Latitude:        in.Latitude,
		Longitude:       in.Longitude,
		Method:          in.Method,
		InvestigatorRef: in.InvestigatorRef,
		Notes:           in.Notes,
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO surveys (survey_id, survey_ref, survey_date, waterbody, site_name, latitude,
		                      longitude, method, investigator_ref, notes, recorded_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		sv.SurveyID, nullable(sv.SurveyRef), sv.SurveyDate, sv.Waterbody, nullable(sv.SiteName),
		nullFloat(sv.Latitude), nullFloat(sv.Longitude), nullable(sv.Method),
		nullable(sv.InvestigatorRef), nullable(sv.Notes), nowText()); err != nil {
		return nil, err
	}
	return sv, nil
}

// GetSurvey 查询单次调查。
func (s *Store) GetSurvey(ctx context.Context, surveyID string) (*Survey, error) {
	sv := &Survey{}
	var surveyRef, siteName, method, investigator, notes sql.NullString
	var lat, lon sql.NullFloat64
	err := s.db.QueryRowContext(ctx,
		`SELECT survey_id, survey_ref, survey_date, waterbody, site_name, latitude, longitude,
		        method, investigator_ref, COALESCE(notes,'')
		 FROM surveys WHERE survey_id=?`, surveyID).
		Scan(&sv.SurveyID, &surveyRef, &sv.SurveyDate, &sv.Waterbody, &siteName, &lat, &lon,
			&method, &investigator, &notes)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sv.SurveyRef = surveyRef.String
	sv.SiteName = siteName.String
	sv.Method = method.String
	sv.InvestigatorRef = investigator.String
	sv.Notes = notes.String
	if lat.Valid {
		sv.Latitude = &lat.Float64
	}
	if lon.Valid {
		sV := lon.Float64
		sv.Longitude = &sV
	}
	return sv, nil
}

// AddRecapture 登记调查中的一次回捕。带标记编码且能匹配到放流批次标记的记为 MATCHED；
// 无标记或匹配不到的记为 UNMATCHED（天然野生鱼或来源不明），永不计入放流成效。
func (s *Store) AddRecapture(ctx context.Context, surveyID string, in RecaptureInput) (*Recapture, error) {
	if _, err := s.GetSurvey(ctx, surveyID); err != nil {
		return nil, err
	}
	if in.SpeciesCode == "" {
		return nil, rulef("species_code 不能为空")
	}
	if in.Count <= 0 {
		return nil, rulef("回捕数量必须大于 0")
	}
	rc := &Recapture{
		RecaptureID: newID("RC"),
		SurveyID:    surveyID,
		MarkCode:    in.MarkCode,
		SpeciesCode: in.SpeciesCode,
		Count:       in.Count,
		MatchStatus: "UNMATCHED",
		Notes:       in.Notes,
	}
	var markID, lotRef sql.NullString
	if in.MarkCode != "" {
		err := s.db.QueryRowContext(ctx,
			`SELECT mark_id, lot_ref FROM marks WHERE mark_code=?`, in.MarkCode).
			Scan(&markID, &lotRef)
		switch {
		case err == nil:
			rc.MatchStatus = "MATCHED"
			rc.MatchedLotRef = lotRef.String
		case errors.Is(err, sql.ErrNoRows):
			// 编码查无此标记：保持 UNMATCHED，不臆断归属。
		default:
			return nil, err
		}
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO recaptures (recapture_id, survey_id, mark_code, species_code, count,
		                         match_status, matched_mark_id, matched_lot_ref, notes)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		rc.RecaptureID, rc.SurveyID, nullable(rc.MarkCode), rc.SpeciesCode, rc.Count,
		rc.MatchStatus, markID, lotRef, nullable(rc.Notes)); err != nil {
		return nil, err
	}
	return rc, nil
}

// RecaptureDetail 在回捕记录上附带调查与批次来源信息。
type RecaptureDetail struct {
	Recapture
	SurveyDate string `json:"survey_date"`
	Waterbody  string `json:"waterbody"`
	SiteName   string `json:"site_name,omitempty"`
	SurveyRef  string `json:"survey_ref,omitempty"`
	LotOrigin  string `json:"lot_origin,omitempty"` // MATCHED 时回填，用于区分野生/增殖
}

// ListRecaptures 返回回捕记录。lotRef 非空时只返回匹配到该放流批次的记录。
func (s *Store) ListRecaptures(ctx context.Context, lotRef string) ([]RecaptureDetail, error) {
	q := `SELECT r.recapture_id, r.survey_id, COALESCE(r.mark_code,''), r.species_code, r.count,
	             r.match_status, COALESCE(r.matched_lot_ref,''),
	             sv.survey_date, sv.waterbody, COALESCE(sv.site_name,''), COALESCE(sv.survey_ref,''),
	             COALESCE(l.origin,'')
	      FROM recaptures r
	      JOIN surveys sv ON sv.survey_id = r.survey_id
	      LEFT JOIN lots l ON l.lot_ref = r.matched_lot_ref`
	args := []any{}
	if lotRef != "" {
		q += " WHERE r.matched_lot_ref=?"
		args = append(args, lotRef)
	}
	q += " ORDER BY sv.survey_date, r.recapture_id"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]RecaptureDetail, 0)
	for rows.Next() {
		var d RecaptureDetail
		if err := rows.Scan(&d.RecaptureID, &d.SurveyID, &d.MarkCode, &d.SpeciesCode, &d.Count,
			&d.MatchStatus, &d.MatchedLotRef, &d.SurveyDate, &d.Waterbody, &d.SiteName,
			&d.SurveyRef, &d.LotOrigin); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
