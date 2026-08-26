# Knowledge Agent Hub design system

The optional UI/UX skill search dataset was unavailable in the current installation, so this system applies its documented desktop-product defaults directly.

## Direction

The interface is a content-first professional workbench: quiet neutral surfaces, a restrained jade accent, strong information hierarchy, and low visual noise. It avoids decorative gradients, glass effects, emoji icons, hover-only affordances, and animation without meaning.

## Foundations

- Semantic CSS tokens define background, surface, border, text, accent, danger, warning, success, focus, and elevation in light and dark themes.
- Segoe UI Variable Text and Microsoft YaHei UI provide native Windows and bilingual readability. Cascadia Code is used only for code.
- Body copy is at least 13px in the dense desktop shell, with 1.5 or greater line height; document content uses 13px at 1.68 line height.
- Interactive icon controls are 36px in the mouse-oriented desktop shell and have visible accessible names. Primary workflow controls are at least 36px high.
- Every interactive control retains a high-contrast `:focus-visible` ring. Dialogs use semantic titles/descriptions, and search/jobs use live status regions.
- Reduced-motion preferences collapse all nonessential animation. Layout does not depend on animation or hover state.

## Layout

- Left: 250px navigation and saved searches.
- Center: flexible search and result workspace.
- Right: minimum 330px evidence/document inspector.
- At narrow supported desktop widths the navigation contracts to 220px and secondary table metadata hides before content does.

