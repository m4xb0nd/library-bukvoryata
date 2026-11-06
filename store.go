package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	LoanDays       = 14 
	MaxActiveLoans = 5  


type Genre struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Author struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type Book struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	AuthorID    int    `json:"author_id"`
	GenreID     int    `json:"genre_id"`
	Year        int    `json:"year"`
	ISBN        string `json:"isbn"`
	Description string `json:"description"`
	Copies      int    `json:"copies"`
}

type Reader struct {
	ID           int       `json:"id"`
	Ticket       string    `json:"ticket"`
	FullName     string    `json:"full_name"`
	Phone        string    `json:"phone"`
	Email        string    `json:"email"`
	RegisteredAt time.Time `json:"registered_at"`
}

type Loan struct {
	ID         int        `json:"id"`
	BookID     int        `json:"book_id"`
	ReaderID   int        `json:"reader_id"`
	IssuedAt   time.Time  `json:"issued_at"`
	DueAt      time.Time  `json:"due_at"`
	ReturnedAt *time.Time `json:"returned_at"`
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

type data struct {
	NextID  map[string]int `json:"next_id"`
	Genres  []Genre        `json:"genres"`
	Authors []Author       `json:"authors"`
	Books   []Book         `json:"books"`
	Readers []Reader       `json:"readers"`
	Loans   []Loan         `json:"loans"`
}

type Store struct {
	mu   sync.RWMutex
	path string
	d    data
}

type ErrUser struct{ Msg string }

func (e ErrUser) Error() string { return e.Msg }

func userErr(format string, a ...any) error { return ErrUser{fmt.Sprintf(format, a...)} }

func IsUserErr(err error) bool {
	var e ErrUser
	return errors.As(err, &e)
}

func OpenStore(path string) (*Store, error) {
	s := &Store{path: path}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		s.seed()
		if err := s.save(); err != nil {
			return nil, err
		}
		return s, nil
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var d data
	if err := json.Unmarshal(raw, &d); err != nil {
		return fmt.Errorf("файл данных %s повреждён: %w", s.path, err)
	}
	if d.NextID == nil {
		d.NextID = map[string]int{}
	}
	s.d = d
	return nil
}

func (s *Store) save() error {
	raw, err := json.MarshalIndent(s.d, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) commit() error {
	if err := s.save(); err != nil {
		_ = s.load()
		return fmt.Errorf("не удалось сохранить данные: %w", err)
	}
	return nil
}

func (s *Store) next(kind string) int {
	s.d.NextID[kind]++
	return s.d.NextID[kind]
}

func (s *Store) seed() {
	s.d = data{NextID: map[string]int{}}
	genres := map[string]int{}
	for _, n := range []string{"Роман", "Повесть", "Рассказ", "Поэзия", "Драма", "Научная литература", "Детская литература"} {
		id := s.next("genre")
		s.d.Genres = append(s.d.Genres, Genre{id, n})
		genres[n] = id
	}
	authors := map[string]int{}
	add := func(title, author, genre string, year, copies int) {
		aid, ok := authors[author]
		if !ok {
			aid = s.next("author")
			s.d.Authors = append(s.d.Authors, Author{aid, author})
			authors[author] = aid
		}
		s.d.Books = append(s.d.Books, Book{
			ID: s.next("book"), Title: title, AuthorID: aid,
			GenreID: genres[genre], Year: year, Copies: copies,
		})
	}
	add("Евгений Онегин", "А. С. Пушкин", "Поэзия", 1833, 3)
	add("Капитанская дочка", "А. С. Пушкин", "Повесть", 1836, 3)
	add("Герой нашего времени", "М. Ю. Лермонтов", "Роман", 1840, 2)
	add("Ревизор", "Н. В. Гоголь", "Драма", 1836, 2)
	add("Преступление и наказание", "Ф. М. Достоевский", "Роман", 1866, 2)
	add("Война и мир", "Л. Н. Толстой", "Роман", 1869, 2)
	add("Вишнёвый сад", "А. П. Чехов", "Драма", 1904, 2)
	add("Мастер и Маргарита", "М. А. Булгаков", "Роман", 1967, 1)

	id := s.next("reader")
	s.d.Readers = append(s.d.Readers, Reader{
		ID: id, Ticket: ticketNumber(id), FullName: "Иванов Иван Иванович",
		Phone: "+7 900 000-00-00", RegisteredAt: time.Now(),
	})
}

func ticketNumber(id int) string { return fmt.Sprintf("ЧБ-%05d", id) }

func (s *Store) activeLoansOfBook(bookID int) int {
	n := 0
	for _, l := range s.d.Loans {
		if l.BookID == bookID && l.ReturnedAt == nil {
			n++
		}
	}
	return n
}

func (s *Store) authorName(id int) string {
	for _, a := range s.d.Authors {
		if a.ID == id {
			return a.Name
		}
	}
	return "—"
}

func (s *Store) genreName(id int) string {
	for _, g := range s.d.Genres {
		if g.ID == id {
			return g.Name
		}
	}
	return "—"
}

func (s *Store) bookView(b Book) BookView {
	return BookView{
		Book: b, Author: s.authorName(b.AuthorID), Genre: s.genreName(b.GenreID),
		Available: b.Copies - s.activeLoansOfBook(b.ID),
	}
}

func (s *Store) findBook(id int) (Book, bool) {
	for _, b := range s.d.Books {
		if b.ID == id {
			return b, true
		}
	}
	return Book{}, false
}

func (s *Store) findReader(id int) (Reader, bool) {
	for _, r := range s.d.Readers {
		if r.ID == id {
			return r, true
		}
	}
	return Reader{}, false
}

func (s *Store) loanView(l Loan) LoanView {
	v := LoanView{Loan: l, BookTitle: "(книга удалена)", ReaderName: "(читатель удалён)"}
	if b, ok := s.findBook(l.BookID); ok {
		v.BookTitle = b.Title
	}
	if r, ok := s.findReader(l.ReaderID); ok {
		v.ReaderName, v.ReaderTicket = r.FullName, r.Ticket
	}
	v.Overdue = l.ReturnedAt == nil && time.Now().After(l.DueAt)
	return v
}

func (s *Store) Genres() []Genre {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Genre(nil), s.d.Genres...)
}

func (s *Store) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	st := Stats{Titles: len(s.d.Books), Readers: len(s.d.Readers)}
	for _, b := range s.d.Books {
		st.Copies += b.Copies
	}
	now := time.Now()
	for _, l := range s.d.Loans {
		if l.ReturnedAt == nil {
			st.ActiveLoans++
			if now.After(l.DueAt) {
				st.OverdueLoans++
			}
		}
	}
	return st
}

func (s *Store) Books(query string, genreID int) []BookView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	q := strings.ToLower(strings.TrimSpace(query))
	var out []BookView
	for _, b := range s.d.Books {
		if genreID > 0 && b.GenreID != genreID {
			continue
		}
		v := s.bookView(b)
		if q != "" && !strings.Contains(strings.ToLower(v.Title), q) &&
			!strings.Contains(strings.ToLower(v.Author), q) {
			continue
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
	})
	return out
}

func (s *Store) Book(id int) (BookView, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.findBook(id)
	if !ok {
		return BookView{}, false
	}
	return s.bookView(b), true
}

func (s *Store) Readers(query string) []ReaderView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	q := strings.ToLower(strings.TrimSpace(query))
	var out []ReaderView
	for _, r := range s.d.Readers {
		if q != "" && !strings.Contains(strings.ToLower(r.FullName), q) &&
			!strings.Contains(strings.ToLower(r.Ticket), q) {
			continue
		}
		n := 0
		for _, l := range s.d.Loans {
			if l.ReaderID == r.ID && l.ReturnedAt == nil {
				n++
			}
		}
		out = append(out, ReaderView{Reader: r, Active: n})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].FullName) < strings.ToLower(out[j].FullName)
	})
	return out
}

func (s *Store) Reader(id int) (ReaderView, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.findReader(id)
	if !ok {
		return ReaderView{}, false
	}
	n := 0
	for _, l := range s.d.Loans {
		if l.ReaderID == id && l.ReturnedAt == nil {
			n++
		}
	}
	return ReaderView{Reader: r, Active: n}, true
}

func (s *Store) Loans(readerID int, onlyActive bool) []LoanView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []LoanView
	for _, l := range s.d.Loans {
		if readerID > 0 && l.ReaderID != readerID {
			continue
		}
		if onlyActive && l.ReturnedAt != nil {
			continue
		}
		out = append(out, s.loanView(l))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IssuedAt.After(out[j].IssuedAt) })
	return out
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

	s.mu.Lock()
	defer s.mu.Unlock()

	genreOK := false
	for _, g := range s.d.Genres {
		if g.ID == genreID {
			genreOK = true
		}
	}
	if !genreOK {
		return 0, userErr("Выберите жанр из списка.")
	}

	authorID := 0
	for _, a := range s.d.Authors {
		if strings.EqualFold(a.Name, author) {
			authorID = a.ID
			break
		}
	}
	if authorID == 0 {
		authorID = s.next("author")
		s.d.Authors = append(s.d.Authors, Author{authorID, author})
	}
	id := s.next("book")
	s.d.Books = append(s.d.Books, Book{
		ID: id, Title: title, AuthorID: authorID, GenreID: genreID,
		Year: year, ISBN: isbn, Description: desc, Copies: copies,
	})
	if err := s.commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Store) DeleteBook(id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := -1
	for i, b := range s.d.Books {
		if b.ID == id {
			idx = i
		}
	}
	if idx < 0 {
		return userErr("Книга не найдена.")
	}
	for _, l := range s.d.Loans {
		if l.BookID == id {
			return userErr("Нельзя удалить книгу: по ней есть записи о выдаче.")
		}
	}
	s.d.Books = append(s.d.Books[:idx], s.d.Books[idx+1:]...)
	return s.commit()
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

	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.next("reader")
	s.d.Readers = append(s.d.Readers, Reader{
		ID: id, Ticket: ticketNumber(id), FullName: fullName,
		Phone: phone, Email: email, RegisteredAt: time.Now(),
	})
	if err := s.commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Store) IssueBook(bookID, readerID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	book, ok := s.findBook(bookID)
	if !ok {
		return userErr("Выберите книгу из списка.")
	}
	if _, ok := s.findReader(readerID); !ok {
		return userErr("Выберите читателя из списка.")
	}

	now := time.Now()
	active, overdue := 0, false
	for _, l := range s.d.Loans {
		if l.ReaderID != readerID || l.ReturnedAt != nil {
			continue
		}
		active++
		if l.BookID == bookID {
			return userErr("Эта книга уже числится за читателем.")
		}
		if now.After(l.DueAt) {
			overdue = true
		}
	}
	switch {
	case overdue:
		return userErr("У читателя есть просроченные книги — выдача невозможна.")
	case active >= MaxActiveLoans:
		return userErr("Лимит выдачи: у читателя уже %d книг на руках.", MaxActiveLoans)
	case book.Copies-s.activeLoansOfBook(bookID) <= 0:
		return userErr("Свободных экземпляров этой книги нет.")
	}

	due := time.Date(now.Year(), now.Month(), now.Day()+LoanDays, 23, 59, 59, 0, now.Location())
	s.d.Loans = append(s.d.Loans, Loan{
		ID: s.next("loan"), BookID: bookID, ReaderID: readerID, IssuedAt: now, DueAt: due,
	})
	return s.commit()
}

func (s *Store) ReturnLoan(loanID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.d.Loans {
		if s.d.Loans[i].ID != loanID {
			continue
		}
		if s.d.Loans[i].ReturnedAt != nil {
			return userErr("Эта книга уже возвращена.")
		}
		t := time.Now()
		s.d.Loans[i].ReturnedAt = &t
		return s.commit()
	}
	return userErr("Запись о выдаче не найдена.")
}
