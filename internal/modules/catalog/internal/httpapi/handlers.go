// Package httpapi serves the catalog tag of api/openapi.yaml. Handlers
// authorize first, take the store only from the context (set by the
// membership middleware, never from the path), and return errs errors
// that the problem package renders.
package httpapi

import (
	"context"
	"fmt"
	"net/http"

	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/catalog/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/authz"
	"github.com/roshbaikfb/iftahonline/internal/platform/errs"
	"github.com/roshbaikfb/iftahonline/internal/platform/problem"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// URLSigner builds delivery URLs of an image master (media.Signer).
type URLSigner interface {
	URLs(objectKey string) map[string]map[string]string
}

// Handlers implements StrictServerInterface.
type Handlers struct {
	svc  *app.Service
	urls URLSigner
	auth authz.Authorizer
}

var _ StrictServerInterface = (*Handlers)(nil)

// Mount registers the catalog routes on mux. scope is the membership
// middleware: it authenticates, returns 404 to non-members, and sets the
// tenant and principal (ADR-0006).
func Mount(mux *http.ServeMux, svc *app.Service, urls URLSigner, auth authz.Authorizer, scope func(http.Handler) http.Handler, resp *problem.Responder) {
	badRequest := func(w http.ResponseWriter, r *http.Request, err error) {
		resp.Write(w, r, errs.BadRequest("invalid request", err))
	}
	strict := NewStrictHandlerWithOptions(&Handlers{svc: svc, urls: urls, auth: auth}, nil, StrictHTTPServerOptions{
		RequestErrorHandlerFunc:  badRequest,
		ResponseErrorHandlerFunc: resp.Write,
	})
	HandlerWithOptions(strict, StdHTTPServerOptions{
		BaseRouter:       mux,
		Middlewares:      []MiddlewareFunc{MiddlewareFunc(scope)},
		ErrorHandlerFunc: badRequest,
	})
}

var (
	errProductNotFound   = errs.NotFound("product_not_found", "product not found")
	errCategoryNotFound  = errs.NotFound("category_not_found", "category not found")
	errImageNotFound     = errs.NotFound("image_not_found", "image not found")
	errSizeChartNotFound = errs.NotFound("size_chart_not_found", "size chart not found")
	errBodyRequired      = errs.BadRequest("request body is required", nil)
)

// pathID parses a path ID. A malformed ID is simply not found.
func pathID(s string, notFound error) (ulid.ID, error) {
	id, err := ulid.Parse(s)
	if err != nil {
		return ulid.ID{}, notFound
	}
	return id, nil
}

// bodyID parses an ID from a request body; a malformed one is a
// validation error on field.
func bodyID(field, s string) (ulid.ID, error) {
	id, err := ulid.Parse(s)
	if err != nil {
		return ulid.ID{}, errs.Validation(errs.FieldError{Field: field, Code: "invalid", Message: "not a valid ID"})
	}
	return id, nil
}

// ListProducts implements StrictServerInterface.
func (h *Handlers) ListProducts(ctx context.Context, req ListProductsRequestObject) (ListProductsResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermCatalogRead); err != nil {
		return nil, err
	}
	q := app.ProductQuery{Cursor: deref(req.Params.Cursor)}
	if req.Params.Status != nil {
		q.Status = string(*req.Params.Status)
	}
	if req.Params.Limit != nil {
		q.Limit = *req.Params.Limit
	}
	if req.Params.Stock != nil {
		q.Stock = string(*req.Params.Stock)
	}
	if req.Params.LowThreshold != nil {
		q.LowThreshold = *req.Params.LowThreshold
		if q.LowThreshold == 0 {
			q.LowThreshold = -1 // 0 is not "the default"
		}
	}
	if req.Params.CategoryId != nil {
		id, err := bodyID("category_id", *req.Params.CategoryId)
		if err != nil {
			return nil, err
		}
		q.CategoryID = &id
	}
	page, err := h.svc.ListProducts(ctx, q)
	if err != nil {
		return nil, err
	}
	out := ListProducts200JSONResponse{Items: make([]Product, len(page.Items))}
	for i, p := range page.Items {
		out.Items[i] = h.toProduct(p)
	}
	if page.NextCursor != "" {
		out.NextCursor = &page.NextCursor
	}
	return out, nil
}

// CreateProduct implements StrictServerInterface.
func (h *Handlers) CreateProduct(ctx context.Context, req CreateProductRequestObject) (CreateProductResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermCatalogManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	in, err := productInput(*req.Body)
	if err != nil {
		return nil, err
	}
	p, err := h.svc.CreateProduct(ctx, in)
	if err != nil {
		return nil, err
	}
	return CreateProduct201JSONResponse(h.toProduct(p)), nil
}

// GetProduct implements StrictServerInterface.
func (h *Handlers) GetProduct(ctx context.Context, req GetProductRequestObject) (GetProductResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermCatalogRead); err != nil {
		return nil, err
	}
	id, err := pathID(req.ProductId, errProductNotFound)
	if err != nil {
		return nil, err
	}
	p, err := h.svc.GetProduct(ctx, id)
	if err != nil {
		return nil, err
	}
	return GetProduct200JSONResponse(h.toProduct(p)), nil
}

// UpdateProduct implements StrictServerInterface.
func (h *Handlers) UpdateProduct(ctx context.Context, req UpdateProductRequestObject) (UpdateProductResponseObject, error) {
	p, err := replace(ctx, h.auth, authz.PermCatalogManage, req.ProductId, errProductNotFound, req.Body, productInput, h.svc.UpdateProduct)
	if err != nil {
		return nil, err
	}
	return UpdateProduct200JSONResponse(h.toProduct(p)), nil
}

// replace runs the steps every PUT of one resource shares: authorize,
// parse the path ID, decode the body, call the use case.
func replace[B, In, Out any](ctx context.Context, auth authz.Authorizer, perm authz.Permission, rawID string, notFound error,
	body *B, decode func(B) (In, error), call func(context.Context, ulid.ID, In) (Out, error)) (Out, error) {
	var zero Out
	if err := auth.Authorize(ctx, perm); err != nil {
		return zero, err
	}
	id, err := pathID(rawID, notFound)
	if err != nil {
		return zero, err
	}
	if body == nil {
		return zero, errBodyRequired
	}
	in, err := decode(*body)
	if err != nil {
		return zero, err
	}
	return call(ctx, id, in)
}

// ListLowStock implements StrictServerInterface.
func (h *Handlers) ListLowStock(ctx context.Context, req ListLowStockRequestObject) (ListLowStockResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermCatalogRead); err != nil {
		return nil, err
	}
	threshold, limit := 5, 5
	if req.Params.Threshold != nil {
		threshold = *req.Params.Threshold
	}
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	items, total, err := h.svc.LowStock(ctx, threshold, limit)
	if err != nil {
		return nil, err
	}
	out := ListLowStock200JSONResponse{Total: total}
	out.Items = make([]struct {
		Available    int      `json:"available"`
		OptionValues []string `json:"option_values"`
		ProductId    ULID     `json:"product_id"`
		ProductTitle string   `json:"product_title"`
		VariantId    ULID     `json:"variant_id"`
	}, len(items))
	for i, it := range items {
		out.Items[i].Available, out.Items[i].OptionValues = it.Available, it.OptionValues
		out.Items[i].ProductId, out.Items[i].ProductTitle, out.Items[i].VariantId = it.ProductID.String(), it.ProductTitle, it.VariantID.String()
	}
	return out, nil
}

// DeleteSampleProducts implements StrictServerInterface.
func (h *Handlers) DeleteSampleProducts(ctx context.Context, _ DeleteSampleProductsRequestObject) (DeleteSampleProductsResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermCatalogManage); err != nil {
		return nil, err
	}
	n, err := h.svc.DeleteSamples(ctx)
	if err != nil {
		return nil, err
	}
	return DeleteSampleProducts200JSONResponse{Deleted: n}, nil
}

// DeleteProduct implements StrictServerInterface.
func (h *Handlers) DeleteProduct(ctx context.Context, req DeleteProductRequestObject) (DeleteProductResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermCatalogManage); err != nil {
		return nil, err
	}
	id, err := pathID(req.ProductId, errProductNotFound)
	if err != nil {
		return nil, err
	}
	if err := h.svc.DeleteProduct(ctx, id); err != nil {
		return nil, err
	}
	return DeleteProduct204Response{}, nil
}

// SetInventory implements StrictServerInterface.
func (h *Handlers) SetInventory(ctx context.Context, req SetInventoryRequestObject) (SetInventoryResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermInventoryManage); err != nil {
		return nil, err
	}
	id, err := pathID(req.VariantId, app.ErrVariantNotFound)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	tracked := req.Body.Tracked == nil || *req.Body.Tracked
	st, err := h.svc.SetStock(ctx, id, domain.StockInput{Tracked: tracked, OnHand: req.Body.OnHand})
	if err != nil {
		return nil, err
	}
	return SetInventory200JSONResponse(toStock(st)), nil
}

// ListCategories implements StrictServerInterface.
func (h *Handlers) ListCategories(ctx context.Context, _ ListCategoriesRequestObject) (ListCategoriesResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermCatalogRead); err != nil {
		return nil, err
	}
	cs, err := h.svc.ListCategories(ctx)
	if err != nil {
		return nil, err
	}
	out := ListCategories200JSONResponse{Items: make([]Category, len(cs))}
	for i, c := range cs {
		out.Items[i] = toCategory(c)
	}
	return out, nil
}

// CreateCategory implements StrictServerInterface.
func (h *Handlers) CreateCategory(ctx context.Context, req CreateCategoryRequestObject) (CreateCategoryResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermCatalogManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	in, err := categoryInput(*req.Body)
	if err != nil {
		return nil, err
	}
	c, err := h.svc.CreateCategory(ctx, in)
	if err != nil {
		return nil, err
	}
	return CreateCategory201JSONResponse(toCategory(c)), nil
}

// UpdateCategory implements StrictServerInterface.
func (h *Handlers) UpdateCategory(ctx context.Context, req UpdateCategoryRequestObject) (UpdateCategoryResponseObject, error) {
	c, err := replace(ctx, h.auth, authz.PermCatalogManage, req.CategoryId, errCategoryNotFound, req.Body, categoryInput, h.svc.UpdateCategory)
	if err != nil {
		return nil, err
	}
	return UpdateCategory200JSONResponse(toCategory(c)), nil
}

// DeleteCategory implements StrictServerInterface.
func (h *Handlers) DeleteCategory(ctx context.Context, req DeleteCategoryRequestObject) (DeleteCategoryResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermCatalogManage); err != nil {
		return nil, err
	}
	id, err := pathID(req.CategoryId, errCategoryNotFound)
	if err != nil {
		return nil, err
	}
	if err := h.svc.DeleteCategory(ctx, id); err != nil {
		return nil, err
	}
	return DeleteCategory204Response{}, nil
}

func productInput(b ProductInput) (domain.ProductInput, error) {
	in := domain.ProductInput{
		Title: b.Title, Slug: deref(b.Slug), Description: deref(b.Description),
		Variants: make([]domain.VariantInput, len(b.Variants)),
	}
	if b.Status != nil {
		in.Status = string(*b.Status)
	}
	if b.Options != nil {
		in.Options = make([]domain.Option, len(*b.Options))
		for i, o := range *b.Options {
			in.Options[i] = domain.Option{Name: o.Name, Values: o.Values}
			if o.Colors != nil {
				in.Options[i].Colors = *o.Colors
			}
		}
	}
	if b.SizeChartId != nil {
		id, err := bodyID("size_chart_id", *b.SizeChartId)
		if err != nil {
			return domain.ProductInput{}, err
		}
		in.SizeChartID = &id
	}
	if b.CategoryIds != nil {
		in.CategoryIDs = make([]ulid.ID, len(*b.CategoryIds))
		for i, s := range *b.CategoryIds {
			id, err := bodyID(fmt.Sprintf("category_ids[%d]", i), s)
			if err != nil {
				return domain.ProductInput{}, err
			}
			in.CategoryIDs[i] = id
		}
	}
	for i, v := range b.Variants {
		dv := domain.VariantInput{SKU: deref(v.Sku), PriceMinor: v.PriceMinor, CompareAtMinor: v.CompareAtMinor, CostMinor: v.CostMinor, Tracked: v.Tracked}
		if v.Hidden != nil {
			dv.Hidden = *v.Hidden
		}
		if v.OptionValues != nil {
			dv.OptionValues = *v.OptionValues
		}
		if v.OnHand != nil {
			dv.OnHand = *v.OnHand
		}
		if v.Id != nil {
			id, err := bodyID(fmt.Sprintf("variants[%d].id", i), *v.Id)
			if err != nil {
				return domain.ProductInput{}, err
			}
			dv.ID = &id
		}
		in.Variants[i] = dv
	}
	return in, nil
}

func categoryInput(b CategoryInput) (domain.CategoryInput, error) {
	in := domain.CategoryInput{Name: b.Name, Slug: deref(b.Slug)}
	if b.Position != nil {
		in.Position = *b.Position
	}
	if b.ParentId != nil {
		id, err := bodyID("parent_id", *b.ParentId)
		if err != nil {
			return domain.CategoryInput{}, err
		}
		in.ParentID = &id
	}
	return in, nil
}

func (h *Handlers) toProduct(p domain.Product) Product {
	out := Product{
		Id: p.ID.String(), Slug: p.Slug, Title: p.Title, Description: p.Description, Status: ProductStatus(p.Status),
		Options: make([]ProductOption, len(p.Options)), Variants: make([]Variant, len(p.Variants)),
		CategoryIds: make([]ULID, len(p.CategoryIDs)), Images: make([]ProductImage, len(p.Images)),
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
	if p.Sample {
		sample := true
		out.Sample = &sample
	}
	for i, img := range p.Images {
		out.Images[i] = h.toImage(img)
	}
	for i, o := range p.Options {
		out.Options[i] = ProductOption{Name: o.Name, Values: o.Values}
		if o.Colors != nil {
			out.Options[i].Colors = &o.Colors
		}
	}
	if p.SizeChartID != nil {
		id := p.SizeChartID.String()
		out.SizeChartId = &id
	}
	for i, v := range p.Variants {
		out.Variants[i] = Variant{
			Id: v.ID.String(), OptionValues: v.OptionValues, PriceMinor: v.Price.Amount(),
			Currency: VariantCurrency(v.Price.Currency()), Position: v.Position, Stock: toStock(v.Stock),
		}
		if v.SKU != "" {
			out.Variants[i].Sku = &v.SKU
		}
		if v.Hidden {
			out.Variants[i].Hidden = &v.Hidden
		}
		if v.CompareAt != nil {
			c := v.CompareAt.Amount()
			out.Variants[i].CompareAtMinor = &c
		}
		if v.Cost != nil {
			c := v.Cost.Amount()
			out.Variants[i].CostMinor = &c
		}
	}
	for i, id := range p.CategoryIDs {
		out.CategoryIds[i] = id.String()
	}
	return out
}

func toStock(s domain.Stock) Stock {
	return Stock{Tracked: s.Tracked, OnHand: s.OnHand, Reserved: s.Reserved, Available: s.Available()}
}

func toCategory(c domain.Category) Category {
	out := Category{Id: c.ID.String(), Slug: c.Slug, Name: c.Name, Position: c.Position, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
	if c.ParentID != nil {
		p := c.ParentID.String()
		out.ParentId = &p
	}
	return out
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// CreateImageUpload implements StrictServerInterface.
func (h *Handlers) CreateImageUpload(ctx context.Context, req CreateImageUploadRequestObject) (CreateImageUploadResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermCatalogManage); err != nil {
		return nil, err
	}
	pid, err := pathID(req.ProductId, errProductNotFound)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	in := domain.ImageInput{Alt: deref(req.Body.Alt)}
	if req.Body.Position != nil {
		in.Position = *req.Body.Position
	}
	img, up, err := h.svc.CreateImageUpload(ctx, pid, string(req.Body.ContentType), in)
	if err != nil {
		return nil, err
	}
	return CreateImageUpload201JSONResponse{Image: h.toImage(img), Upload: PresignedUpload{
		Url: up.URL, Fields: up.Fields, ExpiresAt: up.ExpiresAt, MaxBytes: up.MaxBytes,
	}}, nil
}

// imageIDs authorizes and parses the product and image path IDs.
func (h *Handlers) imageIDs(ctx context.Context, product, image string) (ulid.ID, ulid.ID, error) {
	if err := h.auth.Authorize(ctx, authz.PermCatalogManage); err != nil {
		return ulid.ID{}, ulid.ID{}, err
	}
	pid, err := pathID(product, errImageNotFound)
	if err != nil {
		return ulid.ID{}, ulid.ID{}, err
	}
	iid, err := pathID(image, errImageNotFound)
	return pid, iid, err
}

// CompleteImageUpload implements StrictServerInterface.
func (h *Handlers) CompleteImageUpload(ctx context.Context, req CompleteImageUploadRequestObject) (CompleteImageUploadResponseObject, error) {
	pid, iid, err := h.imageIDs(ctx, req.ProductId, req.ImageId)
	if err != nil {
		return nil, err
	}
	img, err := h.svc.CompleteImageUpload(ctx, pid, iid)
	if err != nil {
		return nil, err
	}
	return CompleteImageUpload202JSONResponse(h.toImage(img)), nil
}

// UpdateImage implements StrictServerInterface.
func (h *Handlers) UpdateImage(ctx context.Context, req UpdateImageRequestObject) (UpdateImageResponseObject, error) {
	pid, iid, err := h.imageIDs(ctx, req.ProductId, req.ImageId)
	if err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	in := domain.ImageInput{Alt: deref(req.Body.Alt)}
	if req.Body.Position != nil {
		in.Position = *req.Body.Position
	}
	img, err := h.svc.UpdateImage(ctx, pid, iid, in)
	if err != nil {
		return nil, err
	}
	return UpdateImage200JSONResponse(h.toImage(img)), nil
}

// DeleteImage implements StrictServerInterface.
func (h *Handlers) DeleteImage(ctx context.Context, req DeleteImageRequestObject) (DeleteImageResponseObject, error) {
	pid, iid, err := h.imageIDs(ctx, req.ProductId, req.ImageId)
	if err != nil {
		return nil, err
	}
	if err := h.svc.DeleteImage(ctx, pid, iid); err != nil {
		return nil, err
	}
	return DeleteImage204Response{}, nil
}

func (h *Handlers) toImage(img domain.Image) ProductImage {
	out := ProductImage{Id: img.ID.String(), Status: ProductImageStatus(img.Status), Alt: img.Alt, Position: img.Position, CreatedAt: img.CreatedAt}
	if img.FailureReason != "" {
		r := ProductImageFailureReason(img.FailureReason)
		out.FailureReason = &r
	}
	if img.Status != domain.ImageReady {
		return out
	}
	w, ht := img.Width, img.Height
	out.Width, out.Height = &w, &ht
	u := h.urls.URLs(img.ObjectKey)
	f := func(preset string) ImageFormats {
		return ImageFormats{Avif: u[preset]["avif"], Webp: u[preset]["webp"]}
	}
	out.Urls = &ImageURLs{Thumb: f("thumb"), Small: f("small"), Medium: f("medium"), Large: f("large")}
	return out
}

// SearchProducts implements StrictServerInterface.
func (h *Handlers) SearchProducts(ctx context.Context, req SearchProductsRequestObject) (SearchProductsResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermCatalogRead); err != nil {
		return nil, err
	}
	p := req.Params
	q := app.SearchQuery{Text: deref(p.Q)}
	if p.Status != nil {
		q.Status = string(*p.Status)
	}
	if p.Limit != nil {
		q.Limit = *p.Limit
	}
	if p.Offset != nil {
		q.Offset = *p.Offset
	}
	if p.CategoryId != nil {
		id, err := bodyID("category_id", *p.CategoryId)
		if err != nil {
			return nil, err
		}
		q.CategoryID = &id
	}
	res, err := h.svc.SearchProducts(ctx, q)
	if err != nil {
		return nil, err
	}
	out := SearchProducts200JSONResponse{Items: make([]ProductSearchHit, len(res.Hits)), EstimatedTotal: res.EstimatedTotal}
	for i, d := range res.Hits {
		hit := ProductSearchHit{
			Id: d.ID, Title: d.Title, Slug: d.Slug, Status: ProductSearchHitStatus(d.Status), CategoryIds: d.CategoryIDs,
			PriceMinor: d.PriceMinor, Currency: ProductSearchHitCurrency(d.Currency),
		}
		if hit.CategoryIds == nil {
			hit.CategoryIds = []ULID{}
		}
		if d.ImageKey != "" {
			u := h.urls.URLs(d.ImageKey)["thumb"]
			hit.Thumb = &ImageFormats{Avif: u["avif"], Webp: u["webp"]}
		}
		out.Items[i] = hit
	}
	return out, nil
}

var errImportNotFound = errs.NotFound("import_not_found", "import not found")

// ListImports implements StrictServerInterface.
func (h *Handlers) ListImports(ctx context.Context, _ ListImportsRequestObject) (ListImportsResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermImportsManage); err != nil {
		return nil, err
	}
	list, err := h.svc.ListImports(ctx)
	if err != nil {
		return nil, err
	}
	out := ListImports200JSONResponse{Items: make([]Import, len(list))}
	for i, im := range list {
		out.Items[i] = toImport(im)
	}
	return out, nil
}

// CreateImport implements StrictServerInterface.
func (h *Handlers) CreateImport(ctx context.Context, req CreateImportRequestObject) (CreateImportResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermImportsManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	in := app.NewImportInput{Kind: string(req.Body.Kind)}
	if w := req.Body.Woocommerce; w != nil {
		in.WooURL, in.WooKey, in.WooSecret = w.Url, w.ConsumerKey, w.ConsumerSecret
	}
	im, up, err := h.svc.CreateImport(ctx, in)
	if err != nil {
		return nil, err
	}
	out := CreateImport201JSONResponse{Import: toImport(im)}
	if up != nil {
		out.Upload = &PresignedUpload{Url: up.URL, Fields: up.Fields, ExpiresAt: up.ExpiresAt, MaxBytes: up.MaxBytes}
	}
	return out, nil
}

// GetImport implements StrictServerInterface.
func (h *Handlers) GetImport(ctx context.Context, req GetImportRequestObject) (GetImportResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermImportsManage); err != nil {
		return nil, err
	}
	id, err := pathID(req.ImportId, errImportNotFound)
	if err != nil {
		return nil, err
	}
	im, err := h.svc.GetImport(ctx, id)
	if err != nil {
		return nil, err
	}
	return GetImport200JSONResponse(toImport(im)), nil
}

// StartImport implements StrictServerInterface.
func (h *Handlers) StartImport(ctx context.Context, req StartImportRequestObject) (StartImportResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermImportsManage); err != nil {
		return nil, err
	}
	id, err := pathID(req.ImportId, errImportNotFound)
	if err != nil {
		return nil, err
	}
	im, err := h.svc.StartImport(ctx, id)
	if err != nil {
		return nil, err
	}
	return StartImport202JSONResponse(toImport(im)), nil
}

func toImport(im domain.Import) Import {
	out := Import{
		Id: im.ID.String(), Kind: ImportKind(im.Kind), Status: ImportStatus(im.Status), Total: im.Total, Processed: im.Cursor,
		Created: im.Created, Updated: im.Updated, Failed: im.Failed, RowErrors: make([]ImportRowError, len(im.RowErrors)),
		CreatedAt: im.CreatedAt, StartedAt: im.StartedAt, FinishedAt: im.FinishedAt,
	}
	for i, e := range im.RowErrors {
		out.RowErrors[i] = ImportRowError{Line: e.Line, Message: e.Message}
	}
	if im.FailureReason != "" {
		out.FailureReason = &im.FailureReason
	}
	if im.WooURL != "" {
		out.WoocommerceUrl = &im.WooURL
	}
	return out
}

// ListSizeCharts implements StrictServerInterface.
func (h *Handlers) ListSizeCharts(ctx context.Context, _ ListSizeChartsRequestObject) (ListSizeChartsResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermCatalogRead); err != nil {
		return nil, err
	}
	cs, err := h.svc.ListSizeCharts(ctx)
	if err != nil {
		return nil, err
	}
	out := ListSizeCharts200JSONResponse{Items: make([]SizeChart, len(cs))}
	for i, c := range cs {
		out.Items[i] = toSizeChart(c)
	}
	return out, nil
}

// GetSizeChart implements StrictServerInterface.
func (h *Handlers) GetSizeChart(ctx context.Context, req GetSizeChartRequestObject) (GetSizeChartResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermCatalogRead); err != nil {
		return nil, err
	}
	id, err := pathID(req.SizeChartId, errSizeChartNotFound)
	if err != nil {
		return nil, err
	}
	c, err := h.svc.GetSizeChart(ctx, id)
	if err != nil {
		return nil, err
	}
	return GetSizeChart200JSONResponse(toSizeChart(c)), nil
}

// CreateSizeChart implements StrictServerInterface.
func (h *Handlers) CreateSizeChart(ctx context.Context, req CreateSizeChartRequestObject) (CreateSizeChartResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermCatalogManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	c, err := h.svc.CreateSizeChart(ctx, sizeChartInput(*req.Body))
	if err != nil {
		return nil, err
	}
	return CreateSizeChart201JSONResponse(toSizeChart(c)), nil
}

// UpdateSizeChart implements StrictServerInterface.
func (h *Handlers) UpdateSizeChart(ctx context.Context, req UpdateSizeChartRequestObject) (UpdateSizeChartResponseObject, error) {
	c, err := replace(ctx, h.auth, authz.PermCatalogManage, req.SizeChartId, errSizeChartNotFound, req.Body,
		func(b SizeChartInput) (domain.SizeChartInput, error) { return sizeChartInput(b), nil }, h.svc.UpdateSizeChart)
	if err != nil {
		return nil, err
	}
	return UpdateSizeChart200JSONResponse(toSizeChart(c)), nil
}

// DeleteSizeChart implements StrictServerInterface.
func (h *Handlers) DeleteSizeChart(ctx context.Context, req DeleteSizeChartRequestObject) (DeleteSizeChartResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermCatalogManage); err != nil {
		return nil, err
	}
	id, err := pathID(req.SizeChartId, errSizeChartNotFound)
	if err != nil {
		return nil, err
	}
	if err := h.svc.DeleteSizeChart(ctx, id); err != nil {
		return nil, err
	}
	return DeleteSizeChart204Response{}, nil
}

func sizeChartInput(b SizeChartInput) domain.SizeChartInput {
	return domain.SizeChartInput{Name: b.Name, Header: b.Header, Body: b.Body}
}

func toSizeChart(c domain.SizeChart) SizeChart {
	return SizeChart{Id: c.ID.String(), Name: c.Name, Header: c.Header, Body: c.Body, Products: c.Products,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt}
}

// CountProducts implements StrictServerInterface.
func (h *Handlers) CountProducts(ctx context.Context, _ CountProductsRequestObject) (CountProductsResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermCatalogRead); err != nil {
		return nil, err
	}
	c, err := h.svc.CountProducts(ctx)
	if err != nil {
		return nil, err
	}
	return CountProducts200JSONResponse{All: c.All, Active: c.Active, Draft: c.Draft, Archived: c.Archived}, nil
}

// BulkProducts implements StrictServerInterface.
func (h *Handlers) BulkProducts(ctx context.Context, req BulkProductsRequestObject) (BulkProductsResponseObject, error) {
	if err := h.auth.Authorize(ctx, authz.PermCatalogManage); err != nil {
		return nil, err
	}
	if req.Body == nil {
		return nil, errBodyRequired
	}
	ids := make([]ulid.ID, len(req.Body.ProductIds))
	for i, s := range req.Body.ProductIds {
		id, err := bodyID(fmt.Sprintf("product_ids[%d]", i), s)
		if err != nil {
			return nil, err
		}
		ids[i] = id
	}
	n, err := h.svc.BulkProducts(ctx, ids, string(req.Body.Action))
	if err != nil {
		return nil, err
	}
	return BulkProducts200JSONResponse{Updated: n}, nil
}
