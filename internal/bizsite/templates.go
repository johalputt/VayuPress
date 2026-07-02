package bizsite

// templates.go — the business-site template catalogue. Each template is a
// design personality: typography, accent colour, section treatments — applied
// over the shared base. All system fonts, flat colour, hairline rules; no
// gradients, no neon. Defaults give the operator a complete, believable site
// to edit rather than an empty form.

// Template is one deployable business-site design.
type Template struct {
	Key           string `json:"key"`
	Name          string `json:"name"`
	Category      string `json:"category"`
	Tagline       string `json:"tagline"` // shown on the picker card
	Eyebrow       string `json:"eyebrow"` // small label above the hero title
	ServicesLabel string `json:"servicesLabel"`
	CSS           string `json:"-"`
	Defaults      Content
}

// ByKey returns the template with the given key, falling back to the first.
func ByKey(key string) Template {
	for _, t := range All() {
		if t.Key == key {
			return t
		}
	}
	return All()[0]
}

// All returns the catalogue in display order.
func All() []Template {
	return []Template{
		{
			Key: "bistro", Name: "Bistro", Category: "Restaurant",
			Tagline: "Warm serif menus and candlelight calm for restaurants and fine dining.",
			Eyebrow: "Restaurant", ServicesLabel: "Menu",
			CSS: `body.vb--bistro{--vb-bg:#faf6f0;--vb-surface:#fffdf9;--vb-accent:#8c4a2f;--vb-radius:4px;font-family:Georgia,Iowan Old Style,Palatino,serif}
body.vb--bistro .vb-hero h1,body.vb--bistro .vb-section h2{font-weight:500}
body.vb--bistro .vb-service{border:0;border-bottom:1px dotted var(--vb-line);border-radius:0;background:none;padding:1rem 0}
body.vb--bistro .vb-services{grid-template-columns:1fr;max-width:44rem}
@media(prefers-color-scheme:dark){body.vb--bistro{--vb-bg:#171310;--vb-surface:#1e1915;--vb-accent:#d98e63}}`,
			Defaults: Content{
				Name: "Maison Olive", Tagline: "Seasonal plates, honest wine, slow evenings.",
				About: "A neighbourhood bistro serving a short, seasonal menu built on local produce.\nBook a table by phone or drop in — the kitchen is open until late.",
				CTA:   "Reserve a table", Hours: "Tue–Sun 18:00–23:00\nClosed Mondays",
				Services: []Service{
					{Title: "Burrata & blood orange", Desc: "Basil oil, smoked salt", Price: "€12"},
					{Title: "Handmade tagliatelle", Desc: "Slow ragù, aged parmesan", Price: "€18"},
					{Title: "Catch of the day", Desc: "Charred lemon, capers", Price: "€24"},
					{Title: "Dark chocolate tart", Desc: "Crème fraîche", Price: "€9"},
				}, ShowBlog: true,
			},
		},
		{
			Key: "roast", Name: "Roast", Category: "Café",
			Tagline: "Bright, friendly and compact — cafés, bakeries and coffee bars.",
			Eyebrow: "Café · Bakery", ServicesLabel: "Menu",
			CSS: `body.vb--roast{--vb-bg:#fffdf7;--vb-surface:#fff;--vb-accent:#b45309;--vb-radius:14px}
body.vb--roast .vb-hero{text-align:left}
body.vb--roast .vb-hero-inner{max-width:72rem;margin:0 auto}
body.vb--roast .vb-hero h1{font-weight:800}
@media(prefers-color-scheme:dark){body.vb--roast{--vb-bg:#141210;--vb-surface:#1c1915;--vb-accent:#f59e0b}}`,
			Defaults: Content{
				Name: "Northside Roast", Tagline: "Small-batch coffee, fresh bakes, good mornings.",
				About: "We roast weekly, bake daily, and pull every shot with care.",
				CTA:   "See the menu", CTALink: "#services", Hours: "Mon–Fri 07:00–17:00\nSat–Sun 08:00–15:00",
				Services: []Service{
					{Title: "Flat white", Price: "£3.40"}, {Title: "Batch filter", Price: "£2.80"},
					{Title: "Sourdough toast", Desc: "Whipped butter, jam", Price: "£4.50"},
					{Title: "Cinnamon knot", Price: "£3.20"},
				}, ShowBlog: true,
			},
		},
		{
			Key: "storefront", Name: "Storefront", Category: "Shop",
			Tagline: "Crisp product grid for retail shops, boutiques and makers.",
			Eyebrow: "Shop", ServicesLabel: "Products",
			CSS: `body.vb--storefront{--vb-bg:#fff;--vb-surface:#fafafa;--vb-accent:#111;--vb-radius:0}
body.vb--storefront .vb-hero h1{font-weight:800;text-transform:uppercase;letter-spacing:-.01em}
body.vb--storefront .vb-nav{border-bottom:2px solid var(--vb-text)}
body.vb--storefront .vb-service{border-width:2px;border-color:var(--vb-text)}
@media(prefers-color-scheme:dark){body.vb--storefront{--vb-bg:#0e0e0e;--vb-surface:#161616;--vb-accent:#fff}}`,
			Defaults: Content{
				Name: "Field & Thread", Tagline: "Everyday goods, made to last.",
				About: "An independent shop stocking durable, repairable, beautiful things.",
				CTA:   "Visit us", Hours: "Mon–Sat 10:00–18:00",
				Services: []Service{
					{Title: "Canvas tote", Desc: "Waxed, 20L", Price: "$48"},
					{Title: "Enamel mug", Desc: "Navy rim", Price: "$18"},
					{Title: "Wool throw", Desc: "Undyed, 130×180", Price: "$120"},
				}, ShowBlog: true,
			},
		},
		{
			Key: "folio", Name: "Folio", Category: "Portfolio",
			Tagline: "Quiet, gallery-first portfolio for designers, photographers and artists.",
			Eyebrow: "Portfolio", ServicesLabel: "Work",
			CSS: `body.vb--folio{--vb-bg:#fcfcfc;--vb-surface:#fff;--vb-accent:#333;--vb-radius:2px}
body.vb--folio .vb-hero{text-align:left;padding-bottom:2.5rem;border-bottom:0}
body.vb--folio .vb-hero h1{font-weight:500;letter-spacing:-.035em}
body.vb--folio .vb-gallery{grid-template-columns:repeat(auto-fill,minmax(300px,1fr))}
body.vb--folio .vb-gallery img{aspect-ratio:1/1}
@media(prefers-color-scheme:dark){body.vb--folio{--vb-bg:#0f0f0f;--vb-surface:#161616;--vb-accent:#ddd}}`,
			Defaults: Content{
				Name: "Mira Sen", Tagline: "Photographer — portraits, places, quiet light.",
				About: "Available for editorial and commercial commissions worldwide.",
				CTA:   "Get in touch",
				Services: []Service{
					{Title: "Portrait session", Desc: "90 minutes, 20 edited frames"},
					{Title: "Editorial day rate", Desc: "Full day on location"},
					{Title: "Prints", Desc: "Archival, editioned"},
				}, ShowBlog: true,
			},
		},
		{
			Key: "studio", Name: "Studio", Category: "Agency",
			Tagline: "Confident and modern for agencies, studios and freelancers.",
			Eyebrow: "Design & Development", ServicesLabel: "Services",
			CSS: `body.vb--studio{--vb-bg:#fafaf8;--vb-surface:#fff;--vb-accent:#4338ca;--vb-radius:12px}
body.vb--studio .vb-hero h1{font-weight:800;letter-spacing:-.04em}
body.vb--studio .vb-eyebrow{font-weight:700}
@media(prefers-color-scheme:dark){body.vb--studio{--vb-bg:#101014;--vb-surface:#17171d;--vb-accent:#818cf8}}`,
			Defaults: Content{
				Name: "North Studio", Tagline: "We design and build calm, fast software.",
				About: "A small senior team. Strategy, identity, product design and engineering — delivered end to end.",
				CTA:   "Start a project",
				Services: []Service{
					{Title: "Brand & identity", Desc: "Naming, identity systems, guidelines"},
					{Title: "Product design", Desc: "Research, UX, UI, prototyping"},
					{Title: "Engineering", Desc: "Web apps, sites, integrations"},
				}, ShowBlog: true,
			},
		},
		{
			Key: "campus", Name: "Campus", Category: "Education",
			Tagline: "Clear and trustworthy for schools, colleges and academies.",
			Eyebrow: "Admissions open", ServicesLabel: "Programmes",
			CSS: `body.vb--campus{--vb-bg:#f8fafc;--vb-surface:#fff;--vb-accent:#1d4ed8;--vb-radius:8px}
body.vb--campus .vb-nav{background:var(--vb-accent)}
body.vb--campus .vb-nav a,body.vb--campus .vb-brand{color:#fff !important}
body.vb--campus .vb-hero h1{font-weight:700}
@media(prefers-color-scheme:dark){body.vb--campus{--vb-bg:#0d1117;--vb-surface:#151b23;--vb-accent:#60a5fa}}`,
			Defaults: Content{
				Name: "Riverside Academy", Tagline: "Small classes. Serious learning. Kind community.",
				About: "An independent school for ages 11–18, focused on depth over drill.\nVisit us on an open morning — dates below.",
				CTA:   "Apply now", Hours: "Office: Mon–Fri 08:00–16:00",
				Services: []Service{
					{Title: "Lower school", Desc: "Ages 11–14 · broad foundation"},
					{Title: "Upper school", Desc: "Ages 15–16 · examination years"},
					{Title: "Sixth form", Desc: "Ages 17–18 · university preparation"},
				}, ShowBlog: true,
			},
		},
		{
			Key: "practice", Name: "Practice", Category: "Clinic",
			Tagline: "Calm and reassuring for clinics, dentists and medical practices.",
			Eyebrow: "Clinic", ServicesLabel: "Treatments",
			CSS: `body.vb--practice{--vb-bg:#f7fbfa;--vb-surface:#fff;--vb-accent:#0f766e;--vb-radius:12px}
body.vb--practice .vb-hero h1{font-weight:600}
@media(prefers-color-scheme:dark){body.vb--practice{--vb-bg:#0e1413;--vb-surface:#151d1b;--vb-accent:#2dd4bf}}`,
			Defaults: Content{
				Name: "Elm Street Clinic", Tagline: "Modern care, unhurried appointments.",
				About: "General practice and specialist referrals. Same-week appointments, clear pricing, no waiting-room chaos.",
				CTA:   "Book an appointment", Hours: "Mon–Fri 08:00–18:00\nSat 09:00–13:00",
				Services: []Service{
					{Title: "General consultation", Desc: "30 minutes", Price: "$60"},
					{Title: "Health check", Desc: "Bloods + review", Price: "$140"},
					{Title: "Vaccinations", Desc: "Travel & seasonal"},
				}, ShowBlog: false,
			},
		},
		{
			Key: "salon", Name: "Salon", Category: "Beauty",
			Tagline: "Elegant and soft for salons, spas and wellness studios.",
			Eyebrow: "Salon & Spa", ServicesLabel: "Treatments",
			CSS: `body.vb--salon{--vb-bg:#fdf9f7;--vb-surface:#fff;--vb-accent:#9d5c63;--vb-radius:18px;font-family:Georgia,Iowan Old Style,Palatino,serif}
body.vb--salon .vb-hero h1{font-weight:500;letter-spacing:-.02em}
body.vb--salon .vb-nav-links{text-transform:uppercase;letter-spacing:.08em;font-size:.8rem}
@media(prefers-color-scheme:dark){body.vb--salon{--vb-bg:#171213;--vb-surface:#1f1919;--vb-accent:#d4959c}}`,
			Defaults: Content{
				Name: "Salt & Silk", Tagline: "Hair, skin and stillness — by appointment.",
				About: "A small studio with an unhurried book. Every visit begins with a consultation.",
				CTA:   "Book now", Hours: "Tue–Sat 09:00–19:00",
				Services: []Service{
					{Title: "Cut & finish", Price: "from $65"},
					{Title: "Colour", Desc: "Balayage, gloss, tone", Price: "from $120"},
					{Title: "Facial", Desc: "60 minutes", Price: "$90"},
				}, ShowBlog: false,
			},
		},
		{
			Key: "forge", Name: "Forge", Category: "Fitness",
			Tagline: "Strong, high-contrast energy for gyms, boxes and coaches.",
			Eyebrow: "Train with us", ServicesLabel: "Programmes",
			CSS: `body.vb--forge{--vb-bg:#101010;--vb-surface:#181818;--vb-text:#f2f2ef;--vb-line:rgba(255,255,255,.12);--vb-accent:#f59e0b;--vb-radius:4px}
body.vb--forge .vb-hero h1{font-weight:800;text-transform:uppercase}
body.vb--forge .vb-cta{color:#111 !important}
@media(prefers-color-scheme:dark){body.vb--forge{--vb-bg:#0c0c0c}}`,
			Defaults: Content{
				Name: "Forge Athletics", Tagline: "Strength first. Everything else follows.",
				About: "Coached strength and conditioning in small groups. First session free.",
				CTA:   "Claim free session", Hours: "Mon–Fri 06:00–21:00\nSat–Sun 08:00–14:00",
				Services: []Service{
					{Title: "Foundations", Desc: "4-week onboarding block", Price: "$99"},
					{Title: "Group training", Desc: "Unlimited classes", Price: "$129/mo"},
					{Title: "1:1 coaching", Desc: "Programming + sessions", Price: "$320/mo"},
				}, ShowBlog: true,
			},
		},
		{
			Key: "counsel", Name: "Counsel", Category: "Professional",
			Tagline: "Understated authority for law firms, accountants and consultants.",
			Eyebrow: "Est. 1998", ServicesLabel: "Practice areas",
			CSS: `body.vb--counsel{--vb-bg:#fbfaf8;--vb-surface:#fff;--vb-accent:#374151;--vb-radius:2px;font-family:Georgia,Cambria,Times New Roman,serif}
body.vb--counsel .vb-hero h1{font-weight:500}
body.vb--counsel .vb-eyebrow{letter-spacing:.3em}
@media(prefers-color-scheme:dark){body.vb--counsel{--vb-bg:#111214;--vb-surface:#191b1e;--vb-accent:#9ca3af}}`,
			Defaults: Content{
				Name: "Harlan & Voss", Tagline: "Counsel for businesses and the people who run them.",
				About: "A boutique practice advising founders, family businesses and estates.\nFirst consultations are without charge.",
				CTA:   "Arrange a consultation", Hours: "Mon–Fri 09:00–17:30",
				Services: []Service{
					{Title: "Commercial", Desc: "Contracts, disputes, advisory"},
					{Title: "Employment", Desc: "Policy, negotiation, tribunals"},
					{Title: "Private client", Desc: "Wills, trusts, succession"},
				}, ShowBlog: false,
			},
		},
		{
			Key: "haven", Name: "Haven", Category: "Hospitality",
			Tagline: "Serene and spacious for hotels, guesthouses and stays.",
			Eyebrow: "Boutique stay", ServicesLabel: "Rooms",
			CSS: `body.vb--haven{--vb-bg:#f9f8f4;--vb-surface:#fffefb;--vb-accent:#65754b;--vb-radius:10px;font-family:Georgia,Iowan Old Style,Palatino,serif}
body.vb--haven .vb-hero{padding:7rem 1.5rem 6rem}
body.vb--haven .vb-hero h1{font-weight:500;letter-spacing:-.02em}
@media(prefers-color-scheme:dark){body.vb--haven{--vb-bg:#12140f;--vb-surface:#1a1d15;--vb-accent:#a3b18a}}`,
			Defaults: Content{
				Name: "The Fern House", Tagline: "Six rooms, one garden, endless quiet.",
				About: "A restored farmhouse at the edge of the valley. Breakfast from the garden, walks from the door.",
				CTA:   "Check availability", Hours: "Check-in 15:00 · Check-out 11:00",
				Services: []Service{
					{Title: "Garden room", Desc: "King bed, terrace", Price: "from $180"},
					{Title: "Valley suite", Desc: "Sitting room, bathtub", Price: "from $260"},
					{Title: "The Loft", Desc: "Sleeps four", Price: "from $320"},
				}, ShowBlog: true,
			},
		},
	}
}
