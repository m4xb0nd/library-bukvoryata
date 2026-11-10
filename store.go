package main

import (
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lib/pq" 
)


const (
	LoanDays       = 14 
	MaxActiveLoans = 5  
)

//go:embed schema.sql
var schemaSQL string

//go:embed data.sql
var dataSQL string



type Genre struct {
	ID   int
	Name string
}

type Book struct {
	ID          int
	Title       string
	AuthorID    int
	GenreID     int
	Year        int
	ISBN        string
	Description string
	Copies      int
}

type Reader struct {
	ID           int
	Ticket       string
	FullName     string
	Phone        string
	Email        string
	RegisteredAt time.Time
}

type Loan struct {
	ID         int
	BookID     int
	ReaderID   int
	IssuedAt   time.Time
	DueAt      time.Time
	ReturnedAt *time.Time 
}


type BookView struct {
	Book
	Author    string
	Genre     string
	Available int 
}

type ReaderView struct {
	Reader
	Active int 
}

type LoanView struct {
	Loan
	BookTitle    string
	ReaderName   string
	ReaderTicket string
	Overdue      bool
}

type Stats struct {
	Titles, Copies, Readers, ActiveLoans, OverdueLoans int
}

type ErrUser struct{ Msg string }

func (e ErrUser) Error() string { return e.Msg }

func userErr(format string, a ...any) error { return ErrUser{fmt.Sprintf(format, a...)} }

func IsUserErr(err error) bool {
	var e ErrUser
	return errors.As(err, &e)
}

func pgCode(err error) string {
	var pe *pq.Error
	if errors.As(err, &pe) {
		return string(pe.Code)
	}
	return ""
}

const (
	codeUnique     = "23505"
	codeForeignKey = "23503"
)

type Store struct {
	db *sql.DB
}


func OpenStore(dsn string) (*Store, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)

	for i := 1; ; i++ {
		if err = db.Ping(); err == nil {
			break
		}
		if i == 30 {
			return nil, fmt.Errorf("не удалось подключиться к базе данных: %w", err)
		}
		log.Println("жду базу данных:", err)
		time.Sleep(time.Second)
	}

	s := &Store{db: db}
	if err := s.initSchema(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) initSchema() error {
	var exists bool
	if err := s.db.QueryRow(`SELECT to_regclass('public.books') IS NOT NULL`).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	log.Println("таблиц нет — создаю схему и начальные данные")

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(schemaSQL); err != nil {
		return fmt.Errorf("schema.sql: %w", err)
	}
	if _, err := tx.Exec(dataSQL); err != nil {
		return fmt.Errorf("data.sql: %w", err)
	}
	return tx.Commit()
}

func likePattern(q string) string {
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(strings.TrimSpace(q))
	return "%" + esc + "%"
}


func (s *Store) Genres() ([]Genre, error) {
	rows, err := s.db.Query(`SELECT genre_id, name FROM genres ORDER BY genre_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Genre
	for rows.Next() {
		var g Genre
		if err := rows.Scan(&g.ID, &g.Name); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) Stats() (Stats, error) {
	var st Stats
	err := s.db.QueryRow(`
		SELECT (SELECT COUNT(*) FROM books),
		       (SELECT COALESCE(SUM(copies_total), 0) FROM books),
		       (SELECT COUNT(*) FROM readers),
		       COUNT(*) FILTER (WHERE returned_at IS NULL),
		       COUNT(*) FILTER (WHERE returned_at IS NULL AND due_at < now())
		FROM loans`).
		Scan(&st.Titles, &st.Copies, &st.Readers, &st.ActiveLoans, &st.OverdueLoans)
	return st, err
}

const bookSelect = `
	SELECT b.book_id, b.title, b.author_id, b.genre_id, b.publication_year,
	       COALESCE(b.isbn, ''), COALESCE(b.description, ''), b.copies_total,
	       a.full_name, g.name,
	       b.copies_total - COUNT(l.loan_id) AS available
	FROM books b
	JOIN authors a ON a.author_id = b.author_id
	JOIN genres  g ON g.genre_id  = b.genre_id
	LEFT JOIN loans l ON l.book_id = b.book_id AND l.returned_at IS NULL`

type scanner interface{ Scan(dest ...any) error }

func scanBook(r scanner) (BookView, error) {
	var v BookView
	err := r.Scan(&v.ID, &v.Title, &v.AuthorID, &v.GenreID, &v.Year, &v.ISBN, &v.Description,
		&v.Copies, &v.Author, &v.Genre, &v.Available)
	return v, err
}


func (s *Store) Books(query string, genreID int) ([]BookView, error) {
	var where []string
	var args []any
	if genreID > 0 {
		args = append(args, genreID)
		where = append(where, fmt.Sprintf("b.genre_id = $%d", len(args)))
	}
	if strings.TrimSpace(query) != "" {
		args = append(args, likePattern(query))
		where = append(where, fmt.Sprintf("(b.title ILIKE $%[1]d OR a.full_name ILIKE $%[1]d)", len(args)))
	}
	q := bookSelect
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " GROUP BY b.book_id, a.full_name, g.name ORDER BY lower(b.title), b.book_id"

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BookView
	for rows.Next() {
		v, err := scanBook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}


func (s *Store) Book(id int) (v BookView, ok bool, err error) {
	v, err = scanBook(s.db.QueryRow(bookSelect+` WHERE b.book_id = $1 GROUP BY b.book_id, a.full_name, g.name`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return v, false, nil
	}
	return v, err == nil, err
}


const readerSelect = `
	SELECT r.reader_id, r.ticket_number, r.full_name, COALESCE(r.phone, ''), COALESCE(r.email, ''),
	       r.registered_at, COUNT(l.loan_id)
	FROM readers r
	LEFT JOIN loans l ON l.reader_id = r.reader_id AND l.returned_at IS NULL`

func scanReader(r scanner) (ReaderView, error) {
	var v ReaderView
	err := r.Scan(&v.ID, &v.Ticket, &v.FullName, &v.Phone, &v.Email, &v.RegisteredAt, &v.Active)
	return v, err
}

func (s *Store) Readers(query string) ([]ReaderView, error) {
	q := readerSelect
	var args []any
	if strings.TrimSpace(query) != "" {
		q += " WHERE r.full_name ILIKE $1 OR r.ticket_number ILIKE $1"
		args = append(args, likePattern(query))
	}
	q += " GROUP BY r.reader_id ORDER BY lower(r.full_name), r.reader_id"

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReaderView
	for rows.Next() {
		v, err := scanReader(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) Reader(id int) (v ReaderView, ok bool, err error) {
	v, err = scanReader(s.db.QueryRow(readerSelect+` WHERE r.reader_id = $1 GROUP BY r.reader_id`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return v, false, nil
	}
	return v, err == nil, err
}


func (s *Store) Loans(readerID int, onlyActive bool) ([]LoanView, error) {
	rows, err := s.db.Query(`
		SELECT l.loan_id, l.book_id, l.reader_id, l.issued_at, l.due_at, l.returned_at,
		       b.title, r.full_name, r.ticket_number,
		       (l.returned_at IS NULL AND l.due_at < now())
		FROM loans l
		JOIN books   b ON b.book_id   = l.book_id
		JOIN readers r ON r.reader_id = l.reader_id
		WHERE ($1::int = 0 OR l.reader_id = $1::int)
		  AND (NOT $2::boolean OR l.returned_at IS NULL)
		ORDER BY l.issued_at DESC, l.loan_id DESC`, readerID, onlyActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LoanView
	for rows.Next() {
		var v LoanView
		var returned sql.NullTime
		if err := rows.Scan(&v.ID, &v.BookID, &v.ReaderID, &v.IssuedAt, &v.DueAt, &returned,
			&v.BookTitle, &v.ReaderName, &v.ReaderTicket, &v.Overdue); err != nil {
			return nil, err
		}
		if returned.Valid {
			t := returned.Time
			v.ReturnedAt = &t
		}
		out = append(out, v)
	}
	return out, rows.Err()
}


func cleanText(s string, max int) (string, bool) {
	s = strings.Join(strings.Fields(s), " ")
	return s, utf8.RuneCountInString(s) <= max
}

func (s *Store) AddBook(title, author string, genreID, year, copies int, isbn, desc string) (int, error) {
	title, ok1 := cleanText(title, 200)
	author, ok2 := cleanText(author, 150)
	isbn, ok3 := cleanText(isbn, 20)
	desc = strings.TrimSpace(desc)
	switch {
	case title == "" || !ok1:
		return 0, userErr("Название обязательно и не должно превышать 200 символов.")
	case author == "" || !ok2:
		return 0, userErr("Автор обязателен и не должен превышать 150 символов.")
	case !ok3:
		return 0, userErr("ISBN слишком длинный.")
	case utf8.RuneCountInString(desc) > 2000:
		return 0, userErr("Описание не должно превышать 2000 символов.")
	case year < 1000 || year > time.Now().Year():
		return 0, userErr("Укажите корректный год издания.")
	case copies < 1 || copies > 1000:
		return 0, userErr("Количество экземпляров должно быть от 1 до 1000.")
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()


	var authorID int
	err = tx.QueryRow(`SELECT author_id FROM authors WHERE lower(full_name) = lower($1)`, author).Scan(&authorID)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.QueryRow(`
			INSERT INTO authors (full_name) VALUES ($1)
			ON CONFLICT (full_name) DO UPDATE SET full_name = EXCLUDED.full_name
			RETURNING author_id`, author).Scan(&authorID)
	}
	if err != nil {
		return 0, err
	}

	var id int
	err = tx.QueryRow(`
		INSERT INTO books (title, author_id, genre_id, publication_year, isbn, description, copies_total)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), $7)
		RETURNING book_id`, title, authorID, genreID, year, isbn, desc, copies).Scan(&id)
	if pgCode(err) == codeForeignKey {
		return 0, userErr("Выберите жанр из списка.")
	}
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func (s *Store) DeleteBook(id int) error {
	res, err := s.db.Exec(`DELETE FROM books WHERE book_id = $1`, id)
	if pgCode(err) == codeForeignKey {
		return userErr("Нельзя удалить книгу: по ней есть записи о выдаче.")
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return userErr("Книга не найдена.")
	}
	return nil
}

func (s *Store) AddReader(fullName, phone, email string) (int, error) {
	fullName, ok1 := cleanText(fullName, 150)
	phone, ok2 := cleanText(phone, 30)
	email, ok3 := cleanText(email, 100)
	switch {
	case utf8.RuneCountInString(fullName) < 3 || !ok1:
		return 0, userErr("Укажите ФИО (от 3 до 150 символов).")
	case !ok2:
		return 0, userErr("Телефон слишком длинный.")
	case !ok3 || (email != "" && !strings.Contains(email, "@")):
		return 0, userErr("Укажите корректный адрес электронной почты или оставьте поле пустым.")
	}

	var id int
	err := s.db.QueryRow(`
		WITH n AS (SELECT nextval(pg_get_serial_sequence('readers', 'reader_id')) AS id)
		INSERT INTO readers (reader_id, ticket_number, full_name, phone, email)
		SELECT id, 'ЧБ-' || lpad(id::text, 5, '0'), $1, NULLIF($2, ''), NULLIF($3, '') FROM n
		RETURNING reader_id`, fullName, phone, email).Scan(&id)
	return id, err
}


func (s *Store) IssueBook(bookID, readerID int) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var copies int
	err = tx.QueryRow(`SELECT copies_total FROM books WHERE book_id = $1 FOR UPDATE`, bookID).Scan(&copies)
	if errors.Is(err, sql.ErrNoRows) {
		return userErr("Выберите книгу из списка.")
	}
	if err != nil {
		return err
	}

	var one int
	err = tx.QueryRow(`SELECT 1 FROM readers WHERE reader_id = $1 FOR UPDATE`, readerID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return userErr("Выберите читателя из списка.")
	}
	if err != nil {
		return err
	}


	var active int
	var overdue, same bool
	err = tx.QueryRow(`
		SELECT COUNT(*),
		       COALESCE(bool_or(due_at < now()), false),
		       COALESCE(bool_or(book_id = $2), false)
		FROM loans WHERE reader_id = $1 AND returned_at IS NULL`, readerID, bookID).Scan(&active, &overdue, &same)
	if err != nil {
		return err
	}

	var taken int 
	err = tx.QueryRow(`SELECT COUNT(*) FROM loans WHERE book_id = $1 AND returned_at IS NULL`, bookID).Scan(&taken)
	if err != nil {
		return err
	}

	switch {
	case same:
		return userErr("Эта книга уже числится за читателем.")
	case overdue:
		return userErr("У читателя есть просроченные книги — выдача невозможна.")
	case active >= MaxActiveLoans:
		return userErr("Лимит выдачи: у читателя уже %d книг на руках.", MaxActiveLoans)
	case copies-taken <= 0:
		return userErr("Свободных экземпляров этой книги нет.")
	}

	now := time.Now()
	due := time.Date(now.Year(), now.Month(), now.Day()+LoanDays, 23, 59, 59, 0, now.Location())
	_, err = tx.Exec(`INSERT INTO loans (book_id, reader_id, due_at) VALUES ($1, $2, $3)`, bookID, readerID, due)
	if pgCode(err) == codeUnique { 
		return userErr("Эта книга уже числится за читателем.")
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ReturnLoan(loanID int) error {
	res, err := s.db.Exec(`UPDATE loans SET returned_at = now() WHERE loan_id = $1 AND returned_at IS NULL`, loanID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return nil
	}

	var returned bool
	err = s.db.QueryRow(`SELECT returned_at IS NOT NULL FROM loans WHERE loan_id = $1`, loanID).Scan(&returned)
	if errors.Is(err, sql.ErrNoRows) {
		return userErr("Запись о выдаче не найдена.")
	}
	if err != nil {
		return err
	}
	return userErr("Эта книга уже возвращена.")
}
