# Vendored third-party assets

These files are compiled into the onWatch binary through `embed.FS` and served
from the dashboard itself. They were previously loaded from `cdn.jsdelivr.net`
and `fonts.googleapis.com`, which disclosed every dashboard viewer's IP address
and User-Agent to those third parties on each page load. Vendoring them keeps
the dashboard self-contained, makes the documented air-gapped operation real,
and removes that disclosure.

Do not replace these with CDN references.

## Chart.js 4.4.1 - MIT

- `chart.umd.min.js`
- Upstream: https://github.com/chartjs/Chart.js
- Copyright (c) 2014-2023 Chart.js Contributors

## chartjs-adapter-date-fns 3.0.0 - MIT

- `chartjs-adapter-date-fns.bundle.min.js` (bundles date-fns, also MIT)
- Upstream: https://github.com/chartjs/chartjs-adapter-date-fns
- Copyright (c) 2022 chartjs-adapter-date-fns Contributors
- Copyright (c) 2021 Sasha Koss and Lesha Koss (date-fns)

### MIT License

```
Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## Ubuntu - SIL Open Font License 1.1

- `fonts/ubuntu-{400,500,700}-{latin,latin-ext}.woff2`
- Upstream: https://fonts.google.com/specimen/Ubuntu
- Copyright 2010-2011 Canonical Ltd, licensed under the Ubuntu Font Licence 1.0
  as distributed by Google Fonts under OFL terms.

## JetBrains Mono - SIL Open Font License 1.1

- `fonts/jetbrains-mono-400-{latin,latin-ext}.woff2` (variable, covers 400-500)
- Upstream: https://github.com/JetBrains/JetBrainsMono
- Copyright 2020 The JetBrains Mono Project Authors

Only the `latin` and `latin-ext` subsets are bundled. Text outside those ranges
falls back to the system stack declared in `--font-primary` / `--font-mono` in
`static/style.css`.

Bundling MIT and OFL assets inside a GPL-3.0 binary is permitted: both licences
are GPL-compatible, and the OFL covers the font files as separate works that the
binary merely distributes. This file satisfies their attribution requirement.
