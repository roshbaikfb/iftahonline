package web

// The English site's copy (/en/). The same rule as the Arabic: what a
// merchant gains, never how the platform is built.

const (
	brandEN   = "iftahonline"
	taglineEN = "An online store platform for cash on delivery, for merchants in Egypt and the Gulf."
)

var enTexts = texts{ //nolint:gochecknoglobals // constant copy
	Skip: "Skip to content", ChatUs: "Chat with us on WhatsApp", StartFree: "Start free", SignIn: "Sign in", Menu: "Menu",
	MainMenu: "Main menu", Features: "Features", Pricing: "Pricing", FAQ: "FAQ", About: "About", Home: "Home", Crumbs: "Breadcrumbs",
	FooterTagline: taglineEN, Platform: "Platform", Contact: "Contact us", WhatsApp: "WhatsApp",
	Rights: "© 2026 iftahonline. All rights reserved.", Legal: "Legal", OtherLang: "ar", OtherLangName: "العربية",
	Arrow: "→", WhatsAppHello: "Hello, I'd like to ask about iftahonline",
	WhatsAppIntl: "WhatsApp (international)", Email: "Email", FollowUs: "Follow us", CompanyNo: "Companies House number:",
	OurStory: "Our story", FoundersEyebrow: "Founders", FoundersH2: "Who founded iftahonline", CoFounder: "Co-founder",
	TeamEyebrow: "The team", TeamH2: "The iftahonline team", ReviewsEyebrow: "Merchants' reviews", ReviewsH2: "What merchants say about iftahonline",
	WatchVideo: "See how iftahonline works", VideoLead: "A quick tour of the platform, from creating a store to the first confirmed order.",

	HomeTitle:     "iftahonline | The online store platform for cash on delivery",
	HomeDesc:      "Create your online store free on iftahonline: order confirmation on WhatsApp, protection from fake orders, and a net profit dashboard. For merchants in Egypt and the Gulf.",
	PricingTitle:  "iftahonline pricing | Start free",
	PricingDesc:   "iftahonline pricing: a free plan forever, no subscription with a share of delivered orders, or monthly and annual plans with a 14-day trial. In pounds in Egypt and dollars elsewhere.",
	FAQTitle:      "iftahonline FAQ",
	FAQDesc:       "Clear answers about iftahonline: starting free, order confirmation on WhatsApp, protection from fake orders, prices in pounds and dollars, and moving your store.",
	FeaturesTitle: "iftahonline features | Everything a cash-on-delivery store needs",
	FeaturesDesc:  "iftahonline features: order confirmation on WhatsApp, protection from fake orders, a net profit dashboard, shipping and collection, a professional store, and marketing tools.",
	AboutTitle:    "About iftahonline",
	AboutDesc:     "iftahonline is an online store platform for cash-on-delivery merchants in Egypt and the Gulf, a brand of Codezilo Ltd, registered in the United Kingdom.",
	NotFoundTitle: "Page not found | iftahonline",

	HeroH1: "The platform that builds your online store", HeroAccent: "for cash on delivery",
	HeroLead:   "Launch your store in minutes, confirm every order on WhatsApp, spot the customer who refuses delivery before you ship, and follow your real net profit. iftahonline, for merchants in Egypt and the Gulf.",
	CreateFree: "Create your store free", SeePrices: "See pricing", HeroFine: "Sign up in a minute · Free plan forever · No card needed · No contract",
	WorksWith:  "Works with tools you know",
	WhyEyebrow: "Why iftahonline", WhyH2: "Built around what costs a cash-on-delivery merchant",
	WhyLead:   "Orders nobody confirms, parcels that come back, profit you can't see. Every tool here was built for one of them.",
	LearnMore: "Learn more about", ToolsEyebrow: "Every tool", ToolsH2: "Everything your store needs, in one place",
	StepsH2: "From sign-up to your first order, the same day", Step: "Step",
	TrustEyebrow: "Safety and trust", TrustH2: "Your store and your data are safe",
	MoveTag: "Free with a plan", MoveH2: "Selling on another platform? We move your store for free",
	MoveLead:       "Subscribe to any paid plan, and our team moves your products, their photos and categories to your new store, without stopping your sales.",
	MoveCTA:        "Ask us to move your store",
	MoveSteps:      [3]string{"Send us your current store's link", "We move the products, photos and categories", "You review your new store and start selling"},
	PricingEyebrow: "Pricing", PricingH2: "Start free, pay as you grow",
	PricingLead: "Prices in your country's currency: pounds in Egypt, dollars elsewhere. Every paid plan starts with a free trial.",
	Recommended: "Recommended", NoHidden: "No hidden fees: you pay Paymob and your carrier directly at their prices, and we take nothing from them.",
	CompareAll: "Compare every plan", FAQEyebrow: "FAQ", FAQH2: "Everything you want to know about iftahonline",
	NoAnswer: "Didn't find your answer?", TalkWhatsApp: "Message us on WhatsApp", CTAH2: "Open your store online today",
	CTALead: "Start on the free plan, and upgrade as your store grows.", TalkToUs: "Talk to us",
	NotFoundH1: "Page not found", NotFoundLead: "The link may have changed. Start from the home page.", HomePage: "Home page",
	PricingH1:    "Clear prices, starting at zero",
	PricingLead2: "Start on the free plan, or with no subscription and pay a share of what's delivered, or choose a monthly or annual plan. Every paid plan starts with a free trial.",
	PricesIn:     "Prices in", CurrencyEGP: "Egyptian pounds", CurrencyUSD: "US dollars", ShowUSD: "Show in dollars", ShowEGP: "Show in pounds",
	Monthly: "Monthly", Annual: "Annual", Cycle: "Billing cycle", Plans: "Plans",
	USDSoon: "Dollar prices are coming soon. To know your country's prices now,", ContactWhatsApp: "chat with us on WhatsApp",
	ExtraNoHidden: "No hidden fees", ExtraNoHiddenBody: "You pay Paymob's card fees and your carrier's rates directly, at their prices. We take nothing from them.",
	ExtraMove: "We move your store free", ExtraMoveBody: "With any paid plan, we move your products from your current platform.",
	ExtraNoContract: "No contract", ExtraNoContractBody: "Cancel any time from your dashboard, and your data stays yours.",
	CompareH2: "Compare the plans in detail", Feature: "Feature", Always: "forever", PerDelivered: "of every order delivered",
	PerMonth: "a month", PerYear: "a year", Soon: "Soon", Trial: "Free trial:",
	FAQH1: "iftahonline FAQ", FAQLead: "What merchants ask before they start: costs, confirmation, protection and moving stores.",
	FeaturesH1:   "Everything a cash-on-delivery store needs",
	FeaturesLead: "From the first order to net profit: tools built for what a cash-on-delivery merchant in Egypt and the Gulf faces every day.",
	PaidOnly:     "On paid plans", ReadMore: "Read more", QuestionsAbout: "Questions about", OtherFeatures: "More iftahonline features",
	AboutH1: "About iftahonline", AboutLead: "We build the tools a cash-on-delivery merchant in Egypt and the Gulf needs to sell more and lose less.",
	WhyH2About: "Why iftahonline?",
	WhyP1:      "In Egypt and the Gulf, many customers pay on delivery. Yet most store platforms were built for card payments: they don't confirm the order with the customer, don't warn you about who refuses delivery, and don't show what your profit loses to shipping, returns and ads.",
	WhyP2:      "We built iftahonline to close that gap: a store in Arabic and English ready in minutes, every order confirmed by its customer on WhatsApp, a rating for every order before you ship it, and a dashboard with your real net profit.",
	ValuesH2:   "What we believe", CompanyH2: "The company", RegisteredUK: "A company registered in the United Kingdom",
	BrandOf: "iftahonline is a brand owned and run by Codezilo Ltd.", ContactLead: "Our team answers you in Arabic or English, from day one.",
	UKFlag: "Flag of the United Kingdom", LastUpdated: "Last updated:",
	PlanFree: "Start free", PlanPays: "Start with no subscription", PlanTrial: "Try it free", PlanSubscribe: "Subscribe",
	Unlimited: "Unlimited", Yes: "Yes", Products: "products", Orders: "orders a month", Messages: "WhatsApp messages a month", Staff: "team members",
	Singular:    map[string]string{"products": "product", "orders a month": "order a month", "WhatsApp messages a month": "WhatsApp message a month", "team members": "team member"},
	ProductsAll: "Unlimited products", OrdersAll: "Unlimited orders", MessagesAll: "Unlimited WhatsApp messages", StaffAll: "Unlimited team",
	Protection: "Fake-order protection", Profit: "Net profit dashboard", Cards: "Card payments", Domain: "Your own domain",
	Example:   "Example",
	ShotLabel: "An example from the merchant dashboard", ShotStore: "Souq store", ShotNav: "Home|Orders|Products|Customer messages|Net profit",
	ShotOrders: "Orders", ShotNew: "New orders", ShotConfirmed: "Confirmed on WhatsApp", ShotHeld: "Held for review",
	ShotRows: [4][3]string{
		{"Mona · Cairo", "Confirmed", "EGP 515"}, {"Ahmed · Giza", "Shipped", "EGP 320"},
		{"Sara · Tanta", "Held for review", "EGP 450"}, {"Khaled · Dubai", "Awaiting confirmation", "AED 149"},
	},
	ToastTitle: "Mona confirmed her order on WhatsApp", ToastOrder: "Order", ToastBody: "is ready to ship",
	ProfitFigures: [5]string{"EGP 18,450", "EGP 62,300", "−EGP 27,900", "−EGP 9,750", "−EGP 6,200"},
	ChatIn1:       "Hi Mona, thanks for your order from Souq store. Order 7P0R1S2T, total EGP 515. Do you confirm it?",
	ChatConfirm:   "Confirm order", ChatCancel: "Cancel order", ChatIn2: "Thanks Mona, your order is confirmed. We'll let you know when it ships.",
	RiskTitle: "Customer rating", RiskHigh: "High risk", RiskWhy: "Refused delivery twice at two other stores in recent months.",
	RiskRule: "Your rule for this level", RiskPrepay: "Ask for delivery fee upfront", RiskHold: "Hold for review", RiskPhone: "Confirm by phone",
	ProfitHead: "Net profit · 30 days", ProfitRevenue: "Revenue", ProfitCost: "Product cost", ProfitShipping: "Shipping and returns", ProfitAds: "Ads",
	ShipTitle: "Shipment for order", ShipStatus: "On the way", ShipConfirmed: "Order confirmed on WhatsApp", ShipPicked: "Picked up by the carrier",
	ShipOnWay: "On the way to Giza", ShipDelivered: "Delivered, EGP 320 collected", ShipFee: "Shipping rates",
	ShipZones:    [4][2]string{{"Cairo and Giza", "EGP 60"}, {"Alexandria and Delta", "EGP 70"}, {"Upper Egypt", "EGP 85"}, {"Over EGP 1,000", "Free shipping"}},
	StoreProduct: "Cotton summer dress", StorePrice: "EGP 450", StoreWas: "EGP 600", StoreName: "Name", StorePhone: "Phone number",
	StoreArea: "Governorate and city", StoreBuy: "Order now, pay on delivery",
	MktPixels: "Ad pixels", MktOn: "Connected", MktOff: "Not connected", MktCoupon: "20% off orders over EGP 500, until the end of the month",
}

var enCopy = siteCopy{ //nolint:gochecknoglobals // constant copy
	brand: brandEN, tagline: taglineEN, legalUpdated: "2 November 2026",
	features: []feature{
		{
			id: "confirm", tag: "Order confirmation", tagClass: "green", more: "/features/whatsapp-confirmation",
			title: "Every order confirmed by its customer on WhatsApp, without a call",
			body:  "Your customer gets their order in a WhatsApp message in your store's name, with «Confirm» and «Cancel» buttons. Customers who aren't on WhatsApp get a text message with a link. Orders nobody answers are cancelled after the time you set.",
			points: []string{
				"Customers confirm themselves, free, from the thank-you page",
				"A log for every message: sent, delivered, read",
				"Automatic cancellation after 1 to 72 hours",
			},
		},
		{
			id: "protect", tag: "Fake-order protection", tagClass: "amber", more: "/features/fake-orders-protection",
			title: "Know who refuses delivery before you ship",
			body:  "Every order gets a rating from delivery results across all iftahonline stores: delivered, refused, no answer. You choose what happens at each level: hold for review, confirm by phone, or ask for the delivery fee upfront. No store sees another store's data, only the rating level.",
		},
		{
			id: "profit", tag: "Net profit", tagClass: "blue", more: "/features/net-profit",
			title: "Sales aren't profit. See what you actually keep",
			body:  "We subtract product cost, shipping, collection fees, returns and ads from your sales, and show your profit for every product, ad campaign and governorate.",
		},
	},
	tools: []tool{
		{"A professional store in Arabic and English", "Three ready designs to choose from, a home page editor with sections, a landing page for every product with one-step ordering, and your own domain.", true},
		{"Shipping and collection", "Rates for every governorate and city, and shipments created and tracked with the carrier.", false},
		{"Card payments", "Connect your own Paymob account, and the money goes straight to you.", false},
		{"Ad pixels", "Meta, TikTok, Snapchat and Google, with more accurate ad results.", false},
		{"Coupons and offers", "A percentage or fixed discount, with a minimum and an end date.", false},
		{"Product catalog", "A product feed that updates itself for Meta, Google and TikTok ads.", false},
		{"Product import", "Move your products from a file or from your old store.", false},
		{"Your team", "Add your staff with permissions: orders, shipping, finance.", false},
	},
	steps: []step{
		{"Create your store", "Sign up with your email, choose your store's name, look and colours, and it gets a link right away."},
		{"Add your products", "With photos, sizes and colours, or import them all at once."},
		{"Start selling", "Share your store's link in your ads, and follow confirmed orders and your profit from one dashboard."},
	},
	trust: []trust{
		{"lock", "Card details never pass through us", "Your customer pays on Paymob's licensed page, and the money goes straight to you."},
		{"people", "Your customers are yours alone", "No other store sees your customers or orders, we never sell their data, and we only message them about their orders from your store."},
		{"download", "Your data is yours", "No contract, cancel any time, and export your products and orders whenever you like."},
		{"chat", "Support on WhatsApp", "A team that answers you in Arabic or English, from day one."},
	},
	worksWith:    []string{"Paymob", "Meeza", "Bosta", "WhatsApp", "Meta", "TikTok", "Snapchat", "Google"},
	featurePages: featurePagesEN,
	values: []step{
		{"Clarity above all", "Published prices, no hidden fees, and real profit numbers, not just sales."},
		{"The merchant owns the store", "No contract, cancel any time, and take your data with you."},
		{"Built for our market", "Arabic first, made for cash on delivery and for the tools our merchants actually use."},
	},
	privacy: legalPage{
		path: "/privacy", name: "Privacy policy", title: "Privacy policy | iftahonline",
		description: "How iftahonline collects, uses and protects data: merchants' data, store customers' data, order ratings, messages, and your rights.",
		h1:          "Privacy policy", lead: "How we collect, use and protect data, in plain words.",
		sections: []legalSection{
			{"Who we are", []string{
				"iftahonline is a brand owned and run by Codezilo Ltd, registered in the United Kingdom, at 5c New Quebec Street, London W1H 7RG, United Kingdom. In this policy we call ourselves «we».",
			}},
			{"The data we collect", []string{
				"About merchants: name, email, phone number and store details, what the merchant enters in their dashboard (products, orders and settings), and billing details. We don't keep card numbers: payment happens on the payment provider's pages.",
				"About store customers: what the customer enters when ordering (name, phone number, address and notes), which we process on behalf of the merchant who owns the store, to fulfil the order.",
				"When visiting stores: cookies needed for the cart to work, and one that keeps the visit's source (such as an ad campaign) for 30 days so the merchant can credit the order to their ad. This website sets no cookies.",
			}},
			{"How we use it", []string{
				"To run stores and fulfil orders: showing the store, recording the order, sending the customer order confirmation messages on WhatsApp or by text, shipping and payment.",
				"To protect merchants from fake orders: we use delivery results (delivered, refused or no answer) linked to an encrypted phone number that can't be read, to give every new order a rating level. No store sees another store's orders or customers' data, only the rating level. We delete these results after [the set period].",
				"For billing, support and improving the service, and to comply with the law.",
			}},
			{"Who we share it with", []string{
				"We never sell data. We share only what's needed with providers who work to deliver the service: payment providers (such as Paymob), the carriers the merchant chooses, messaging providers (WhatsApp and text messages), hosting and email providers, and the ad platforms the merchant connects to their store.",
			}},
			{"Protecting data", []string{
				"We encrypt sensitive data at rest and in transit, no store can reach another store's data, and we never write full phone numbers in our logs.",
			}},
			{"How long we keep it", []string{
				"We keep a store's data while the account exists. If a store is closed for non-payment we keep its data for [the period] so its owner can come back, then delete it. [Other retention periods are set by the lawyer.]",
			}},
			{"Your rights", []string{
				"You may ask for a copy of your data, its correction or its deletion. Store customers contact the store they ordered from first, and may contact us too. Reach us on the WhatsApp number or the email shown on this site.",
			}},
			{"Changes to this policy", []string{
				"We may update this policy; the last update date is shown at its top, and we tell merchants about important changes.",
			}},
		},
	},
	terms: legalPage{
		path: "/terms", name: "Terms of service", title: "Terms of service | iftahonline",
		description: "The terms for using iftahonline: your account, plans and payment, merchant responsibilities, content, suspension and cancellation.",
		h1:          "Terms of service", lead: "The agreement between you and us when you use iftahonline.",
		sections: []legalSection{
			{"The service", []string{
				"iftahonline is a platform that lets merchants create and run online stores. It is run by Codezilo Ltd, registered in the United Kingdom. By using the platform you agree to these terms.",
			}},
			{"Your account", []string{
				"You keep your account details accurate and your password safe, and you are responsible for what is done from your account and your team's accounts.",
			}},
			{"Plans and payment", []string{
				"Prices, plans and their limits are as published on the pricing page when you subscribe. The free plan is permanent within its limits. Paid plans are paid in advance for each month or year, and may start with a free trial. On «Pay as you sell», a published share of the products' value is charged on every delivered order.",
				"A change of paid plan starts at the next renewal. If a renewal isn't paid, the store gets seven days' grace, then closes with its data kept until it's paid.",
				"Payment providers' and carriers' fees are paid by the merchant directly to them, at their prices.",
			}},
			{"Your responsibilities as a merchant", []string{
				"You are responsible for your products, their prices and descriptions, for fulfilling your customers' orders, and for complying with the laws that apply to your business and your customers' data. Selling what the law forbids or what harms others is not allowed.",
			}},
			{"Content", []string{
				"What you put in your store (text, images and products) is yours. You give us permission to host and show it only to run your store.",
			}},
			{"Suspension and cancellation", []string{
				"You can cancel your subscription any time from your dashboard. We may suspend a store that breaks these terms or the law, after notifying you where possible.",
			}},
			{"Limitation of liability", []string{
				"[Limitation of liability and indemnity clauses are written by the lawyer.]",
			}},
			{"Governing law", []string{
				"[The governing law and competent courts are set by the lawyer.]",
			}},
			{"Contact", []string{
				"For any question about these terms, reach us on the WhatsApp number or the email shown on this site.",
			}},
		},
	},
}

// featurePagesEN translate features.go's pages.
var featurePagesEN = []featurePage{ //nolint:gochecknoglobals,dupl // constant copy; the English of features.go, the same shape by design
	{
		slug: "whatsapp-confirmation", name: "Order confirmation on WhatsApp", art: "confirm",
		short:       "Every order confirmed by its customer on WhatsApp in one tap, and a text message for those not on WhatsApp.",
		title:       "Confirm cash-on-delivery orders on WhatsApp automatically | iftahonline",
		description: "Confirm cash-on-delivery orders on WhatsApp automatically: a message in your store's name with Confirm and Cancel buttons, a text message fallback, and automatic cancellation of unanswered orders. On iftahonline.",
		h1:          "Confirm cash-on-delivery orders on WhatsApp, automatically",
		lead:        "Every new order in your iftahonline store reaches its customer on WhatsApp with «Confirm order» and «Cancel order» buttons. The customer answers in one tap, and the order is confirmed or cancelled in your dashboard without you calling anyone.",
		howTitle:    "How confirmation works",
		how: []step{
			{"The customer confirms first", "The thank-you page has a «Confirm on WhatsApp» button: the customer sends the ready message, and the order is confirmed at once, without using one of your messages."},
			{"Otherwise, the confirmation message", "After ten minutes without an answer, they get a WhatsApp message in your store's name with the order details and the two buttons."},
			{"A text message if they're not on WhatsApp", "If their number isn't on WhatsApp, they get a text message with a link to a page where they confirm or cancel."},
			{"Unanswered orders cancel themselves", "You set the time, from 1 to 72 hours. An order not confirmed in time is cancelled and its stock returns."},
		},
		controlTitle: "You're in control",
		control: []step{
			{"Your own text message wording", "Write the text message in Arabic and English as you like."},
			{"Every message logged", "For every order you see whether the message was sent, delivered and read, and how the customer answered."},
			{"Phone confirmation when needed", "For a customer who refused delivery before, you can choose to call them yourself before shipping, even if they confirmed."},
		},
		faqs: []faq{
			{q: "Do I need my own WhatsApp Business number?", a: "No. Messages are sent from iftahonline's approved number, and every message names your store."},
			{q: "How many confirmation messages does each plan include?", a: "Each plan has a monthly number of messages shown on the pricing page, and we tell you when you're close to the limit."},
			{q: "What if the customer doesn't answer?", a: "The order is cancelled after the time you set. If the message wasn't delivered at all, the order isn't cancelled and you're flagged to call the customer."},
		},
	},
	{
		slug: "fake-orders-protection", name: "Fake-order protection", art: "protect", paid: true,
		short:       "Know the customer who refuses delivery before you ship, and choose what happens to their order.",
		title:       "Protection from fake orders and refused deliveries | iftahonline",
		description: "Cut fake orders and refused deliveries in cash on delivery: a rating for every order from delivery results across all iftahonline stores, and rules you choose for each level.",
		h1:          "Know who refuses delivery before you ship",
		lead:        "Every refused parcel costs you shipping both ways. iftahonline rates every order from the delivery results of the customer's number across all its stores, and you choose what happens at each level.",
		howTitle:    "How protection works",
		how: []step{
			{"A rating for every order", "Every order gets a level, new, low, medium or high, from the past delivery results of the customer's number: delivered, refused, no answer."},
			{"Privacy for every store", "No store sees another store's orders or customers, only the rating level and the customer's history with your own store."},
			{"Your rules for each level", "You choose from which level each action starts: hold the order for review, confirm it by phone, or ask for the delivery fee upfront."},
			{"Delivery fee upfront", "A high-risk customer gets a link to pay the delivery fee, and pays the rest on delivery. The order isn't confirmed before they pay."},
		},
		controlTitle: "You stay the one who decides",
		control: []step{
			{"You release held orders", "A held order is in your dashboard, and you confirm it once you're sure."},
			{"New customers aren't punished", "Customers without a history are never held or questioned, so you don't lose new customers."},
			{"Your customer sees nothing", "The customer never sees a rating or a reason, and the page after ordering is the same for everyone."},
		},
		faqs: []faq{
			{q: "Where does a customer's rating come from?", a: "From the delivery results of their number's orders across all iftahonline stores: delivered, refused or no answer. Recent results count more."},
			{q: "Can another store see my customers' data?", a: "No. No store sees another store's orders or customers, only the rating level."},
			{q: "Which plans include protection?", a: "Paid plans; the details are on the pricing page."},
		},
	},
	{
		slug: "net-profit", name: "Net profit dashboard", art: "profit", paid: true,
		short:       "What you actually keep after cost, shipping, returns and ads, for every product and campaign.",
		title:       "A net profit dashboard for your online store | iftahonline",
		description: "Know your store's real net profit: iftahonline subtracts product cost, shipping, collection fees, returns and ads, for every product, ad campaign and governorate.",
		h1:          "Sales aren't profit. Know what you actually keep",
		lead:        "With cash on delivery, profit leaks between shipping, returns and ads. iftahonline's net profit dashboard subtracts them all from your sales, and shows where you win and where you lose.",
		howTitle:    "What the dashboard subtracts from your sales",
		how: []step{
			{"Product cost", "Record each product's cost once, or import it with your products, and it's subtracted from every delivered order."},
			{"Shipping and collection fees", "From your own estimate first, then from what the carrier actually charged once it arrives."},
			{"Returns", "The cost of a parcel that comes back both ways, so you see what refusals do to your profit."},
			{"Ads", "Record what you spent on each campaign, and it's subtracted from that campaign's profit."},
		},
		controlTitle: "Your profit, from every angle",
		control: []step{
			{"By product", "Know which product really earns and which one eats your profit."},
			{"By ad campaign", "Know which campaign deserves a bigger budget, and which to stop."},
			{"By governorate", "See where returns climb before you lose more there."},
		},
		faqs: []faq{
			{q: "How often does the dashboard update?", a: "Every few minutes, with every order that moves and every cost you record."},
			{q: "What if I didn't record a product's cost?", a: "The dashboard flags orders with products without a cost, because their profit shows higher than it really is."},
			{q: "Which plans include the dashboard?", a: "Paid plans; the details are on the pricing page."},
		},
	},
	{
		slug: "shipping-cod", name: "Shipping and collection", art: "shipping",
		short:       "Shipping rates for every governorate and city, shipments created and tracked, and collection matched.",
		title:       "Shipping and cash on delivery for your store | iftahonline",
		description: "Run shipping and cash on delivery from one place: rates for every governorate and city, collection fees, shipments and waybills with the carrier, customer tracking, and matching what the carrier collected.",
		h1:          "Shipping and cash on delivery, from one dashboard",
		lead:        "Set shipping rates and collection fees as you like, create shipments with the carrier straight from your orders, follow every parcel until it arrives, and match what the carrier collected with your orders.",
		howTitle:    "From order to collection",
		how: []step{
			{"Rates for every governorate and city", "Shipping zones with their rates, a different rate for specific cities, and free shipping above an amount you set."},
			{"Cash-on-delivery fees", "A fixed amount or a percentage, shown clearly to the customer before they order."},
			{"Shipments and waybills", "Create the shipment with the carrier from the order itself, print the waybill, and the order's status follows the parcel."},
			{"Collection matching", "Upload the carrier's collection statement, and see what reached you and what didn't."},
		},
		controlTitle: "And your customer follows their order",
		control: []step{
			{"A tracking page in your store", "The customer tracks their order by its number and their phone, without asking you."},
			{"Address by governorate and city", "An order form designed for addresses in Egypt and the Gulf."},
			{"Card payments too", "Connect your own Paymob account so whoever wants can pay by card, and the money goes straight to you."},
		},
		faqs: []faq{
			{q: "Which carriers does the platform work with?", a: "With Bosta in Egypt: shipments are created and tracked from your dashboard. With any other carrier you handle shipping yourself and move the order's status from your dashboard."},
			{q: "Does the customer see the cash-on-delivery fee before ordering?", a: "Yes, they see the shipping fee, the collection fee and the total before they confirm their order."},
		},
	},
	{
		slug: "online-store", name: "A professional store and landing pages", art: "store",
		short:       "Three ready designs, a home page editor, and landing pages with one-step ordering.",
		title:       "Create your online store and landing pages in Arabic | iftahonline",
		description: "Create a professional online store in Arabic and English in minutes: three ready designs, a home page editor, a landing page for every product with one-step ordering, and your own domain.",
		h1:          "A professional store in Arabic, ready in minutes",
		lead:        "Choose your store's look and colours, add your products, and start selling. Every product has a landing page where the customer orders in one step, ideal for your ads.",
		howTitle:    "Everything your store needs",
		how: []step{
			{"Three ready designs", "Pick the one closest to your brand, and change the colours, font and corners as you like."},
			{"A home page editor", "Arrange its sections: a main banner, categories, products, offers, reviews and a countdown."},
			{"Landing pages with one-step ordering", "A page for every product with its photos, benefits and order form, because every extra step loses a customer."},
			{"In Arabic and English", "Your store in both languages, in your country's currency."},
		},
		controlTitle: "And more",
		control: []step{
			{"Products with sizes and colours", "With size charts, and stock for every size and colour."},
			{"Product import", "From a CSV file or a WooCommerce store, all at once."},
			{"Your own domain", "Connect your domain on paid plans, and your store keeps a free iftahonline link."},
		},
		faqs: []faq{
			{q: "Do I need design or coding experience?", a: "No. A setup wizard guides you step by step, and everything is in your dashboard."},
			{q: "Can I move my products from another platform?", a: "Yes, from a CSV file or a WooCommerce store. With any paid plan we move them for you, free."},
		},
	},
	{
		slug: "marketing", name: "Ads and marketing", art: "marketing",
		short:       "Meta, TikTok, Snapchat and Google pixels, a product catalog, and coupons.",
		title:       "Ad pixels and marketing for your store | iftahonline",
		description: "Market your store with confidence: Meta, TikTok, Snapchat and Google pixels with more accurate ad results, a product catalog that updates itself, and discount coupons.",
		h1:          "Your ads know what happens in your store",
		lead:        "Connect your ad platforms' pixels in a minute, measure your ads more accurately, send your products to catalog ads, and build your offers with coupons.",
		howTitle:    "Marketing tools",
		how: []step{
			{"Ad pixels", "Meta, TikTok, Snapchat and Google: paste the pixel ID, and visits and orders are sent to them."},
			{"More accurate results", "Orders reach the ad platforms even when browsers block them, so your ads learn faster."},
			{"Product catalog", "A product feed that updates itself for Meta, Google and TikTok ads."},
			{"Coupons", "A percentage or fixed discount, with a minimum, an end date and a number of uses."},
		},
		faqs: []faq{
			{q: "Do I need a developer to connect a pixel?", a: "No. Paste the pixel ID in your dashboard and it works at once."},
			{q: "Does the ad know which campaign brought the order?", a: "Yes, each order keeps its source and campaign, shown per campaign in the net profit dashboard."},
		},
	},
}
