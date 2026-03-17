# LineSpec Documentation Site

This is the source for the LineSpec documentation website hosted on GitHub Pages.

## Development

To work on this site locally:

```bash
# Clone the repository
git clone https://github.com/livecodelife/linespec.git
cd linespec/docs

# Serve locally (if you have Jekyll installed)
bundle exec jekyll serve

# Or simply open index.html in your browser for static preview
open index.html
```

## Deployment

This site is automatically deployed to GitHub Pages when changes are pushed to the main branch.

### GitHub Pages Setup

1. Go to your repository settings on GitHub
2. Navigate to "Pages" in the left sidebar
3. Under "Build and deployment", select:
   - **Source**: Deploy from a branch
   - **Branch**: `main` / `docs` folder
4. Click "Save"

Your site will be available at: `https://livecodelife.github.io/linespec`

### Custom Domain (Optional)

To use a custom domain:

1. Create a `CNAME` file in this directory with your domain:
   ```
   www.linespec.dev
   ```
2. Configure your DNS provider to point to GitHub Pages
3. Enable HTTPS in repository settings

## Structure

```
docs/
├── index.html          # Main landing page
├── _config.yml         # Jekyll/GitHub Pages configuration
├── CNAME              # Custom domain (optional)
├── assets/
│   ├── css/
│   │   └── style.css  # Main stylesheet
│   └── js/
│       └── main.js    # Interactive features
└── README.md          # This file
```

## Design

The site uses a modern dark theme with:
- Inter font for body text
- JetBrains Mono for code
- Indigo/purple gradient accents
- Responsive design for all devices

## Updating Content

The content is static HTML. To update:

1. Edit `index.html` for content changes
2. Edit `assets/css/style.css` for styling changes
3. Edit `assets/js/main.js` for interactive features

## License

MIT License - Same as the LineSpec project
