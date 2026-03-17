/**
 * LineSpec Docs - Shared Components
 * Reduces code duplication across documentation pages
 */

(function() {
    'use strict';

    // SVG Icons
    const Icons = {
        logo: `<svg width="32" height="32" viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg">
            <rect width="32" height="32" rx="6" fill="url(#gradient)"/>
            <path d="M8 12h16M8 16h12M8 20h8" stroke="white" stroke-width="2" stroke-linecap="round"/>
            <defs>
                <linearGradient id="gradient" x1="0" y1="0" x2="32" y2="32" gradientUnits="userSpaceOnUse">
                    <stop stop-color="#47591B"/>
                    <stop offset="1" stop-color="#6b8022"/>
                </linearGradient>
            </defs>
        </svg>`,

        logoSmall: `<svg width="24" height="24" viewBox="0 0 32 32" fill="none" xmlns="http://www.w3.org/2000/svg">
            <rect width="32" height="32" rx="6" fill="url(#gradient-footer)"/>
            <path d="M8 12h16M8 16h12M8 20h8" stroke="white" stroke-width="2" stroke-linecap="round"/>
            <defs>
                <linearGradient id="gradient-footer" x1="0" y1="0" x2="32" y2="32" gradientUnits="userSpaceOnUse">
                    <stop stop-color="#47591B"/>
                    <stop offset="1" stop-color="#6b8022"/>
                </linearGradient>
            </defs>
        </svg>`,

        document: `<svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/>
            <polyline points="14 2 14 8 20 8"/>
            <line x1="16" y1="13" x2="8" y2="13"/>
            <line x1="16" y1="17" x2="8" y2="17"/>
            <polyline points="10 9 9 9 8 9"/>
        </svg>`,

        menu: `<svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <line x1="3" y1="12" x2="21" y2="12"/>
            <line x1="3" y1="6" x2="21" y2="6"/>
            <line x1="3" y1="18" x2="21" y2="18"/>
        </svg>`,

        close: `<svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <line x1="18" y1="6" x2="6" y2="18"/>
            <line x1="6" y1="6" x2="18" y2="18"/>
        </svg>`,

        copy: `<svg width="16" height="16" viewBox="0 0 16 16" fill="none">
            <path d="M4 4v8h8V4H4zM2 2h12v12H2V2z" stroke="currentColor" stroke-width="1.5"/>
        </svg>`,

        search: `<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <circle cx="11" cy="11" r="8"/>
            <path d="M21 21l-4.35-4.35"/>
        </svg>`,

        chevronRight: `<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polyline points="9 18 15 12 9 6"/>
        </svg>`
    };

    // Navigation Links Configuration
    const NavLinks = {
        main: [
            { href: 'index.html#features', label: 'Features' },
            { href: 'provenance.html', label: 'Provenance' },
            { href: 'index.html#semantic-search-feature', label: 'AI Search' },
            { href: 'index.html#testing', label: 'Testing' },
            { href: 'docs.html', label: 'Docs' },
            { href: 'https://github.com/livecodelife/linespec', label: 'GitHub', external: true }
        ],
        docs: [
            { href: 'index.html#features', label: 'Features' },
            { href: 'docs.html', label: 'Docs', active: true },
            { href: 'https://github.com/livecodelife/linespec', label: 'GitHub', external: true }
        ]
    };

    // Render Navigation Links
    function renderNavLinks(links, isMobile = false) {
        return links.map(link => {
            const activeClass = link.active ? 'active' : '';
            const externalAttrs = link.external ? 'target="_blank" rel="noopener"' : '';
            const btnClass = link.label === 'Get Started' ? 'btn btn-primary' : '';
            
            if (isMobile) {
                return `<a href="${link.href}" class="mobile-nav-link ${activeClass}" ${externalAttrs}>${link.label}</a>`;
            }
            return `<a href="${link.href}" class="${activeClass} ${btnClass}" ${externalAttrs}>${link.label}</a>`;
        }).join('');
    }

    // Create Main Navigation (for index.html and landing pages)
    function createMainNav() {
        return `
            <nav class="navbar">
                <div class="container">
                    <a href="index.html" class="logo">
                        ${Icons.logo}
                        <span>LineSpec</span>
                    </a>
                    <div class="nav-links">
                        ${renderNavLinks(NavLinks.main)}
                    </div>
                    <button class="mobile-menu-btn" aria-label="Toggle menu" id="mobile-menu-toggle">
                        <span></span>
                        <span></span>
                        <span></span>
                    </button>
                </div>
                <div class="mobile-menu" id="mobile-menu">
                    ${renderNavLinks(NavLinks.main, true)}
                </div>
            </nav>
        `;
    }

    // Create Docs Navigation (for documentation pages)
    function createDocsNav() {
        return `
            <nav class="navbar docs-navbar">
                <div class="container">
                    <a href="index.html" class="logo">
                        ${Icons.logo}
                        <span>LineSpec</span>
                    </a>
                    <div class="nav-links">
                        ${renderNavLinks(NavLinks.docs)}
                    </div>
                    <button class="mobile-menu-btn" aria-label="Toggle menu" id="mobile-menu-toggle">
                        <span></span>
                        <span></span>
                        <span></span>
                    </button>
                </div>
                <div class="mobile-menu" id="mobile-menu">
                    ${renderNavLinks(NavLinks.docs, true)}
                </div>
            </nav>
        `;
    }

    // Create Footer
    function createFooter() {
        return `
            <footer class="footer">
                <div class="container">
                    <div class="footer-content">
                        <div class="footer-brand">
                            <a href="index.html" class="logo">
                                ${Icons.logoSmall}
                                <span>LineSpec</span>
                            </a>
                            <p>Structured decisions. Deterministic testing.</p>
                        </div>
                        <div class="footer-links">
                            <div class="footer-column">
                                <h4>Product</h4>
                                <a href="index.html#features">Features</a>
                                <a href="provenance.html">Provenance Records</a>
                                <a href="linespec.html">LineSpec Testing</a>
                                <a href="https://github.com/livecodelife/linespec/releases" target="_blank">Changelog</a>
                            </div>
                            <div class="footer-column">
                                <h4>Resources</h4>
                                <a href="docs.html">Documentation</a>
                                <a href="provenance.html">Provenance Guide</a>
                                <a href="linespec.html">DSL Reference</a>
                                <a href="https://github.com/livecodelife/linespec/tree/main/examples" target="_blank">Examples</a>
                            </div>
                            <div class="footer-column">
                                <h4>Community</h4>
                                <a href="https://github.com/livecodelife/linespec" target="_blank">GitHub</a>
                                <a href="https://github.com/livecodelife/linespec/issues" target="_blank">Issues</a>
                                <a href="https://github.com/livecodelife/linespec/discussions" target="_blank">Discussions</a>
                            </div>
                        </div>
                    </div>
                    <div class="footer-bottom">
                        <p>MIT License · © 2026 LineSpec Contributors</p>
                    </div>
                </div>
            </footer>
        `;
    }

    // Expose Components globally
    window.LineSpecComponents = {
        Icons,
        createMainNav,
        createDocsNav,
        createFooter,
        renderNavLinks
    };
})();
