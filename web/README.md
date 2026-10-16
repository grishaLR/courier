# courier.social (web)

The Astro site for [courier.social](https://courier.social): the ATmosphere App Catalog, plus the privacy and terms pages. See the [root README](../README.md) for an overview of Courier.

The catalog data lives in `public/app_catalog.json`. The Go server also seeds its app registry from this file.

```sh
npm install
npm run dev        # http://localhost:4321
npm run build      # outputs to ./dist/
```

Deployed to Vercel.
