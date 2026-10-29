// Renders the price list page from the product data.
"use strict";
const fs = require("fs");

const head = fs.readFileSync("partials/head.html", "utf8");
const foot = fs.readFileSync("partials/foot.html", "utf8");
const products = JSON.parse(fs.readFileSync("data/products.json", "utf8"));

const listed = products.filter((p) => p.active).sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
const rows = listed.map((p) => `      <tr><td>${p.name}</td><td>${(p.price_cents / 100).toFixed(2)}</td></tr>`);

fs.mkdirSync("public", { recursive: true });
fs.writeFileSync("public/prices.html", head + rows.join("\n") + "\n" + foot);
console.log(`wrote public/prices.html: ${listed.length} products`);
