package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Built-in category slugs. The heuristic categorizer maps to these slugs;
// user-renamed names still work because slug stays stable.
const (
	CategoryFocus      = "focus"
	CategoryOneOnOne   = "one_on_one"
	CategoryTeam       = "team"
	CategoryExternal   = "external"
	CategoryPersonal   = "personal"
	CategoryTravel     = "travel"
	CategoryOther      = "other"
	CategoryFree       = "free"
)

type Category struct {
	ID        int64
	Slug      string
	Name      string
	Color     string
	SortOrder int
	Builtin   bool
	CreatedAt time.Time
}

type CategoryRepo struct{ pool *pgxpool.Pool }

func NewCategoryRepo(pool *pgxpool.Pool) *CategoryRepo { return &CategoryRepo{pool: pool} }

func (r *CategoryRepo) List(ctx context.Context) ([]Category, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, slug, name, color, sort_order, builtin, created_at
		FROM category ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCategories(rows)
}

func (r *CategoryRepo) Get(ctx context.Context, id int64) (*Category, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, slug, name, color, sort_order, builtin, created_at
		FROM category WHERE id = $1`, id)
	return scanCategoryRow(row)
}

func (r *CategoryRepo) GetBySlug(ctx context.Context, slug string) (*Category, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, slug, name, color, sort_order, builtin, created_at
		FROM category WHERE slug = $1`, slug)
	return scanCategoryRow(row)
}

// UpdateAppearance lets users tweak the display name and color without
// touching the slug (which is the stable identifier engines reference).
func (r *CategoryRepo) UpdateAppearance(ctx context.Context, id int64, name, color string) error {
	_, err := r.pool.Exec(ctx, `UPDATE category SET name = $2, color = $3 WHERE id = $1`, id, name, color)
	return err
}

func scanCategoryRow(row pgx.Row) (*Category, error) {
	var c Category
	if err := row.Scan(&c.ID, &c.Slug, &c.Name, &c.Color, &c.SortOrder, &c.Builtin, &c.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

func scanCategories(rows pgx.Rows) ([]Category, error) {
	var out []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Slug, &c.Name, &c.Color, &c.SortOrder, &c.Builtin, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
