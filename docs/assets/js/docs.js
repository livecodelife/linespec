// LineSpec Documentation Site - Additional Interactive Features

document.addEventListener('DOMContentLoaded', () => {
    // Tab switching for docs install commands
    initDocsTabs();
    
    // Sidebar search
    initSidebarSearch();
    
    // Highlight current section in sidebar
    highlightCurrentSection();
    
    // Smooth scroll for anchor links in docs
    initDocsSmoothScroll();
});

// Tab Switching for Docs
function initDocsTabs() {
    const tabContainers = document.querySelectorAll('.install-commands');
    
    tabContainers.forEach(container => {
        const tabButtons = container.querySelectorAll('.tab');
        const tabPanels = container.querySelectorAll('.command-panel');
        
        tabButtons.forEach(button => {
            button.addEventListener('click', () => {
                const tabId = button.dataset.tab;
                
                // Update active states
                tabButtons.forEach(btn => btn.classList.remove('active'));
                tabPanels.forEach(panel => panel.classList.remove('active'));
                
                button.classList.add('active');
                const panel = container.querySelector(`#${tabId}`);
                if (panel) {
                    panel.classList.add('active');
                }
            });
        });
    });
}

// Sidebar Search
function initSidebarSearch() {
    const searchInput = document.getElementById('docs-search');
    if (!searchInput) return;
    
    searchInput.addEventListener('input', (e) => {
        const query = e.target.value.toLowerCase();
        const links = document.querySelectorAll('.sidebar-nav a');
        
        links.forEach(link => {
            const text = link.textContent.toLowerCase();
            const listItem = link.closest('li');
            
            if (text.includes(query)) {
                link.style.display = 'block';
                if (listItem) listItem.style.display = 'block';
            } else {
                if (query === '') {
                    link.style.display = 'block';
                    if (listItem) listItem.style.display = 'block';
                } else {
                    link.style.display = 'none';
                    if (listItem) listItem.style.display = 'none';
                }
            }
        });
        
        // Show/hide sections based on visible links
        document.querySelectorAll('.nav-section').forEach(section => {
            const visibleLinks = section.querySelectorAll('a[style="display: block;"], a:not([style*="none"])');
            if (visibleLinks.length === 0 && query !== '') {
                section.style.display = 'none';
            } else {
                section.style.display = 'block';
            }
        });
    });
}

// Highlight Current Section in Sidebar
function highlightCurrentSection() {
    const sections = document.querySelectorAll('.docs-section[id]');
    const sidebarLinks = document.querySelectorAll('.sidebar-nav a[href^="#"]');
    
    if (sections.length === 0 || sidebarLinks.length === 0) return;
    
    const observerOptions = {
        root: null,
        rootMargin: '-20% 0px -80% 0px',
        threshold: 0
    };
    
    const observer = new IntersectionObserver((entries) => {
        entries.forEach(entry => {
            if (entry.isIntersecting) {
                const id = entry.target.getAttribute('id');
                
                // Remove active class from all links
                sidebarLinks.forEach(link => link.classList.remove('active'));
                
                // Add active class to current section link
                const currentLink = document.querySelector(`.sidebar-nav a[href="#${id}"]`);
                if (currentLink) {
                    currentLink.classList.add('active');
                }
            }
        });
    }, observerOptions);
    
    sections.forEach(section => observer.observe(section));
}

// Docs Smooth Scroll
function initDocsSmoothScroll() {
    document.querySelectorAll('.docs-toc a[href^="#"], .sidebar-nav a[href^="#"]').forEach(anchor => {
        anchor.addEventListener('click', function(e) {
            e.preventDefault();
            const targetId = this.getAttribute('href').substring(1);
            const target = document.getElementById(targetId);
            
            if (target) {
                const navbarHeight = 64;
                const sidebarOffset = window.innerWidth > 1024 ? 0 : 0;
                const offsetTop = target.offsetTop - navbarHeight - 20;
                
                window.scrollTo({
                    top: offsetTop,
                    behavior: 'smooth'
                });
                
                // Update URL hash
                history.pushState(null, null, `#${targetId}`);
            }
        });
    });
}

// Handle initial hash on page load
window.addEventListener('load', () => {
    if (window.location.hash) {
        const targetId = window.location.hash.substring(1);
        const target = document.getElementById(targetId);
        
        if (target) {
            setTimeout(() => {
                const navbarHeight = 64;
                const offsetTop = target.offsetTop - navbarHeight - 20;
                
                window.scrollTo({
                    top: offsetTop,
                    behavior: 'smooth'
                });
            }, 100);
        }
    }
});
