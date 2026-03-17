/**
 * LineSpec Documentation Site - Interactive Features
 */

document.addEventListener('DOMContentLoaded', () => {
    // Initialize all features
    initTabs();
    initCopyButtons();
    initMobileMenu();
    initSmoothScroll();
    initDocsSidebar();
    initNavbarScroll();
});

/**
 * Tab Switching for install commands
 */
function initTabs() {
    const tabButtons = document.querySelectorAll('.tab');
    const tabPanels = document.querySelectorAll('.command-panel');
    
    tabButtons.forEach(button => {
        button.addEventListener('click', () => {
            const tabId = button.dataset.tab;
            
            // Update active states
            tabButtons.forEach(btn => btn.classList.remove('active'));
            tabPanels.forEach(panel => panel.classList.remove('active'));
            
            button.classList.add('active');
            document.getElementById(tabId)?.classList.add('active');
        });
    });
}

/**
 * Copy to Clipboard functionality
 */
function initCopyButtons() {
    const copyButtons = document.querySelectorAll('.copy-btn');
    
    copyButtons.forEach(button => {
        button.addEventListener('click', async () => {
            // Find the code block - check for new structure first (.install-code-inner)
            let codeBlock = button.parentElement?.querySelector('.install-code-inner code, .install-code-inner pre');
            
            // Fallback to old structure or previous sibling
            if (!codeBlock) {
                codeBlock = button.previousElementSibling || button.parentElement?.querySelector('code, pre');
            }
            
            const textToCopy = codeBlock ? codeBlock.textContent.trim() : '';
            
            try {
                await navigator.clipboard.writeText(textToCopy);
                showCopySuccess(button);
            } catch (err) {
                console.error('Failed to copy:', err);
                // Fallback for older browsers
                fallbackCopyText(textToCopy, button);
            }
        });
    });
}

/**
 * Show copy success feedback
 */
function showCopySuccess(button) {
    const originalHTML = button.innerHTML;
    button.innerHTML = `
        <svg width="16" height="16" viewBox="0 0 16 16" fill="none">
            <path d="M13.5 4L6 11.5L2.5 8" stroke="#7a8f4a" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
        </svg>
    `;
    button.classList.add('copied');
    
    setTimeout(() => {
        button.innerHTML = originalHTML;
        button.classList.remove('copied');
    }, 2000);
}

/**
 * Fallback copy for older browsers
 */
function fallbackCopyText(text, button) {
    const textArea = document.createElement('textarea');
    textArea.value = text;
    textArea.style.position = 'fixed';
    textArea.style.left = '-999999px';
    document.body.appendChild(textArea);
    
    try {
        textArea.select();
        document.execCommand('copy');
        showCopySuccess(button);
    } catch (err) {
        console.error('Fallback copy failed:', err);
    } finally {
        document.body.removeChild(textArea);
    }
}

/**
 * Mobile Menu Toggle with animation
 */
function initMobileMenu() {
    const menuBtn = document.getElementById('mobile-menu-toggle');
    const mobileMenu = document.getElementById('mobile-menu');
    
    if (!menuBtn || !mobileMenu) return;
    
    // Toggle menu on button click
    menuBtn.addEventListener('click', () => {
        const isOpen = mobileMenu.classList.contains('active');
        
        if (isOpen) {
            closeMobileMenu(menuBtn, mobileMenu);
        } else {
            openMobileMenu(menuBtn, mobileMenu);
        }
    });
    
    // Close menu when clicking a link
    mobileMenu.querySelectorAll('a').forEach(link => {
        link.addEventListener('click', () => {
            closeMobileMenu(menuBtn, mobileMenu);
        });
    });
    
    // Close menu when clicking outside
    document.addEventListener('click', (e) => {
        if (!menuBtn.contains(e.target) && !mobileMenu.contains(e.target)) {
            closeMobileMenu(menuBtn, mobileMenu);
        }
    });
    
    // Close menu on escape key
    document.addEventListener('keydown', (e) => {
        if (e.key === 'Escape' && mobileMenu.classList.contains('active')) {
            closeMobileMenu(menuBtn, mobileMenu);
        }
    });
}

function openMobileMenu(btn, menu) {
    btn.classList.add('active');
    menu.classList.add('active');
    document.body.style.overflow = 'hidden';
}

function closeMobileMenu(btn, menu) {
    btn.classList.remove('active');
    menu.classList.remove('active');
    document.body.style.overflow = '';
}

/**
 * Documentation Sidebar for mobile
 */
function initDocsSidebar() {
    const sidebar = document.querySelector('.sidebar');
    if (!sidebar) return;
    
    // Create sidebar toggle for mobile
    const docsContent = document.querySelector('.docs-content');
    if (!docsContent) return;
    
    // Check if we're on a docs page
    const isDocsPage = document.body.classList.contains('docs-page');
    if (!isDocsPage) return;
    
    // Create mobile sidebar toggle button
    const sidebarToggle = document.createElement('button');
    sidebarToggle.className = 'sidebar-toggle';
    sidebarToggle.setAttribute('aria-label', 'Toggle documentation menu');
    sidebarToggle.innerHTML = `
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <line x1="3" y1="12" x2="21" y2="12"/>
            <line x1="3" y1="6" x2="21" y2="6"/>
            <line x1="3" y1="18" x2="21" y2="18"/>
        </svg>
        <span>Menu</span>
    `;
    
    // Insert before the main content
    docsContent.insertBefore(sidebarToggle, docsContent.firstChild);
    
    // Add overlay
    const overlay = document.createElement('div');
    overlay.className = 'sidebar-overlay';
    document.body.appendChild(overlay);
    
    // Toggle sidebar
    sidebarToggle.addEventListener('click', () => {
        sidebar.classList.toggle('mobile-open');
        overlay.classList.toggle('active');
        document.body.style.overflow = sidebar.classList.contains('mobile-open') ? 'hidden' : '';
    });
    
    // Close on overlay click
    overlay.addEventListener('click', () => {
        sidebar.classList.remove('mobile-open');
        overlay.classList.remove('active');
        document.body.style.overflow = '';
    });
    
    // Close on escape
    document.addEventListener('keydown', (e) => {
        if (e.key === 'Escape' && sidebar.classList.contains('mobile-open')) {
            sidebar.classList.remove('mobile-open');
            overlay.classList.remove('active');
            document.body.style.overflow = '';
        }
    });
    
    // Close when clicking a link in sidebar
    sidebar.querySelectorAll('a').forEach(link => {
        link.addEventListener('click', () => {
            if (window.innerWidth <= 1024) {
                sidebar.classList.remove('mobile-open');
                overlay.classList.remove('active');
                document.body.style.overflow = '';
            }
        });
    });
}

/**
 * Smooth Scroll for anchor links
 */
function initSmoothScroll() {
    document.querySelectorAll('a[href^="#"]').forEach(anchor => {
        anchor.addEventListener('click', function(e) {
            const href = this.getAttribute('href');
            if (href === '#') return;
            
            const target = document.querySelector(href);
            if (!target) return;
            
            e.preventDefault();
            
            const headerOffset = 80;
            const elementPosition = target.getBoundingClientRect().top;
            const offsetPosition = elementPosition + window.pageYOffset - headerOffset;
            
            window.scrollTo({
                top: offsetPosition,
                behavior: 'smooth'
            });
            
            // Update URL without jumping
            history.pushState(null, null, href);
        });
    });
}

/**
 * Navbar scroll effect
 */
function initNavbarScroll() {
    const navbar = document.querySelector('.navbar');
    if (!navbar) return;
    
    let lastScroll = 0;
    const scrollThreshold = 50;
    
    window.addEventListener('scroll', () => {
        const currentScroll = window.pageYOffset;
        
        // Add background on scroll
        if (currentScroll > scrollThreshold) {
            navbar.classList.add('scrolled');
        } else {
            navbar.classList.remove('scrolled');
        }
        
        lastScroll = currentScroll;
    }, { passive: true });
}

/**
 * Intersection Observer for scroll animations
 */
if ('IntersectionObserver' in window) {
    const observerOptions = {
        root: null,
        rootMargin: '0px',
        threshold: 0.1
    };
    
    const observer = new IntersectionObserver((entries) => {
        entries.forEach(entry => {
            if (entry.isIntersecting) {
                entry.target.classList.add('animate-in');
            }
        });
    }, observerOptions);
    
    // Observe elements with animation class
    document.querySelectorAll('.feature-card, .cli-card, .step').forEach(el => {
        el.classList.add('animate-on-scroll');
        observer.observe(el);
    });
}
