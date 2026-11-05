// Package site is the platform's own public website on the platform's
// domain (ADR-0071): what افتح أونلاين is, its plans and its answers,
// server-rendered for search engines and AI assistants (robots.txt,
// sitemap.xml, llms.txt, schema.org data). It reads the plans on sale
// from billing and serves no fonts of its own (the shop's brand
// typeface). Its contact details and FAQ are edited from the super-admin
// console (S4a, ADR-0073). This file is the module's only exported
// surface (ADR-0001); see docs/modules/site.md.
package site

import (
	"context"

	"github.com/roshbaikfb/iftahonline/internal/modules/billing"
	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/app"
	"github.com/roshbaikfb/iftahonline/internal/modules/site/internal/domain"
	"github.com/roshbaikfb/iftahonline/internal/platform/ulid"
)

// DTOs.
type (
	// Settings are the site's contact details.
	Settings = domain.Settings
	// Social is a network the site links to.
	Social = domain.Social
	// FAQ is one question, in Arabic and optionally English.
	FAQ = domain.FAQ
	// Content is everything the site shows from the console.
	Content = domain.Content
	// Story is the founding story and the video.
	Story = domain.Story
	// Image is one of the site's own images (the logo, the share image).
	Image = domain.Image
	// Person is a founder or a team member.
	Person = domain.Person
	// PersonKind is a founder or a team member.
	PersonKind = domain.PersonKind
	// Testimonial is a merchant's real review.
	Testimonial = domain.Testimonial
	// Upload is where the console's browser posts an image.
	Upload = app.Upload
	// Guide is one of the site's guides (S4c).
	Guide = domain.Guide
	// GuideStatus is a guide's draft or published.
	GuideStatus = domain.GuideStatus
)

// The site's images and the kinds of people.
const (
	Logo       = domain.Logo
	ShareImage = domain.ShareImage
	Founder    = domain.Founder
	Team       = domain.Team
	Draft      = domain.Draft
	Published  = domain.Published
)

// The networks.
const (
	Facebook  = domain.Facebook
	Instagram = domain.Instagram
	TikTok    = domain.TikTok
	LinkedIn  = domain.LinkedIn
	YouTube   = domain.YouTube
	X         = domain.X
)

// ParseVideo reads a YouTube link (or a bare ID) into the video's ID.
func ParseVideo(link string) (string, error) { return domain.ParseVideo(link) }

// Socials lists every network, in the site's order.
func Socials() []Social { return domain.Socials() }

// MaxFAQs bounds the FAQ.
const MaxFAQs = domain.MaxFAQs

// Contacts are the platform's contact details for the other modules
// (NewContacts): the dashboard sends an agency's store to its WhatsApp.
type Contacts interface {
	// Contact is the platform's contact details.
	Contact(ctx context.Context) (Settings, error)
}

var _ Contacts = (*app.Service)(nil)

// Console is what the super-admin console edits. Changes reach the
// site's pages within a minute, then the edge's cache lifetime.
type Console interface {
	Settings(ctx context.Context) (Settings, error)
	// SaveSettings checks the details and returns them as saved.
	SaveSettings(ctx context.Context, s Settings) (Settings, error)
	// FAQs are the questions in order.
	FAQs(ctx context.Context) ([]FAQ, error)
	// CreateFAQ adds a question and returns it with its ID.
	CreateFAQ(ctx context.Context, f FAQ) (FAQ, error)
	UpdateFAQ(ctx context.Context, f FAQ) (FAQ, error)
	DeleteFAQ(ctx context.Context, id ulid.ID) error

	// All is everything the site shows, read now.
	All(ctx context.Context) (Content, error)
	// SaveStory writes the founding story and the video (a YouTube link
	// or "").
	SaveStory(ctx context.Context, ar, en, videoLink string) (Story, error)
	// UploadURL is a presigned upload of an image (JPEG, PNG or WebP).
	UploadURL(ctx context.Context, contentType string) (Upload, error)
	// SetImage makes an upload the logo or the share image ("" removes
	// it) and returns its key.
	SetImage(ctx context.Context, which Image, uploadKey string) (string, error)
	// CreatePerson and UpdatePerson take an uploaded photo ("" for none;
	// on update, keep it unless removePhoto).
	CreatePerson(ctx context.Context, p Person, uploadKey string) (Person, error)
	UpdatePerson(ctx context.Context, p Person, uploadKey string, removePhoto bool) (Person, error)
	DeletePerson(ctx context.Context, id ulid.ID) error
	CreateTestimonial(ctx context.Context, t Testimonial, uploadKey string) (Testimonial, error)
	UpdateTestimonial(ctx context.Context, t Testimonial, uploadKey string, removePhoto bool) (Testimonial, error)
	DeleteTestimonial(ctx context.Context, id ulid.ID) error
	// ImageURL is an image's signed URL for the console's thumbnails.
	ImageURL(key string, size int, format string) string

	// Guides are every guide, newest first, without their bodies; Guide
	// is one, whole.
	Guides(ctx context.Context) ([]Guide, error)
	Guide(ctx context.Context, id ulid.ID) (Guide, error)
	// CreateGuide and UpdateGuide take an uploaded cover as the people's
	// photos; a guide published is dated then, and keeps that date.
	CreateGuide(ctx context.Context, g Guide, uploadKey string) (Guide, error)
	UpdateGuide(ctx context.Context, g Guide, uploadKey string, removeCover bool) (Guide, error)
	DeleteGuide(ctx context.Context, id ulid.ID) error
	// Preview is Markdown as the site would show it (safe HTML).
	Preview(ctx context.Context, markdown string) (string, error)
}

var _ Console = (*app.Service)(nil)

// Plans lists the plans on sale (billing.NewShowcase).
type Plans = billing.Showcase

// Font is the brand typeface the shop serves (storefront.Shop.BrandFont):
// its @font-face rules, and the files to preload. The site's stylesheet
// names its family.
type Font struct {
	Faces   string
	Preload []string
}

// DefaultWhatsApp is the platform's support number (the owner's, Egypt):
// what the site shows until it first reads the console's settings.
const DefaultWhatsApp = "+201153450716"

// DefaultCountryHeader is where the CDN puts the visitor's country
// (Cloudflare's ISO 3166-1 alpha-2 code): Egypt is priced in pounds,
// every other country in dollars.
const DefaultCountryHeader = "CF-IPCountry"
