package clip

// 图片 OCR 的两条存储边：写结果（含搜索索引）与找待识别的条目。
//
// 关键约束：**写 OCR 结果不能动 `updated_at`**。
// 那个字段是"内容变过"的等价物（同步指纹、时间线排序、冲突仲裁都用它），识别结果
// 属于派生数据，碰它会让"刚开机"看起来像"所有图片刚刚都被编辑过"。

// SetEntryOCR writes the recognition result and refreshes this entry's search index.
//
// state 用 OcrState* 常量；text 可以为空（图里确实没字）。
func (s *Store) SetEntryOCR(id, text, state string) error {
	e, ok := s.GetEntry(id)
	if !ok {
		return errEntryMissing
	}
	if e.OcrText == text && e.OcrState == state {
		return nil
	}
	e.OcrText, e.OcrState = text, state

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE entries SET ocr_text = ?, ocr_state = ? WHERE id = ?`,
		text, state, id); err != nil {
		return err
	}
	// OCR 文本要靠 FTS 才能搜到：索引行必须跟着重建。
	if err := indexEntryTx(tx, e); err != nil {
		return err
	}
	return tx.Commit()
}

// EntryIDsNeedingOCR lists image entries that have not been recognized yet,
// newest first (recent screenshots are what people search for).
func (s *Store) EntryIDsNeedingOCR(limit int) []string {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.Query(
		`SELECT id FROM entries
		  WHERE kind = ? AND ocr_state = '' AND archived = 0
		  ORDER BY created_at DESC LIMIT ?`, string(KindImage), limit)
	if err != nil {
		Logf(s.dir, "list entries needing ocr failed: %v", err)
		return nil
	}
	defer rows.Close()
	out := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// OCRStats reports how many entries are waiting / done / failed (界面与日志用).
func (s *Store) OCRStats() (done, pending, failed int) {
	row := s.db.QueryRow(
		`SELECT
		   SUM(CASE WHEN ocr_state = ? THEN 1 ELSE 0 END),
		   SUM(CASE WHEN ocr_state = ? THEN 1 ELSE 0 END),
		   SUM(CASE WHEN ocr_state IN (?, ?) THEN 1 ELSE 0 END)
		 FROM entries WHERE kind = ?`,
		OcrStateDone, OcrStatePending, OcrStateFailed, OcrStateUnsupported, string(KindImage))
	var d, p, f *int
	if err := row.Scan(&d, &p, &f); err != nil {
		return 0, 0, 0
	}
	pick := func(v *int) int {
		if v == nil {
			return 0
		}
		return *v
	}
	return pick(d), pick(p), pick(f)
}
