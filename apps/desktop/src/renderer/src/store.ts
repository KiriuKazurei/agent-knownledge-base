import { create } from 'zustand'
import { persist } from 'zustand/middleware'

interface SavedSearch { id: string; name: string; query: string; libraryId?: string }
interface UIState {
  theme: 'light'|'dark'|'system'
  density: 'comfortable'|'compact'
  savedSearches: SavedSearch[]
  setTheme(theme: UIState['theme']): void
  toggleDensity(): void
  saveSearch(search: SavedSearch): void
  removeSearch(id: string): void
}

export const useUI = create<UIState>()(persist((set) => ({
  theme: 'system', density: 'comfortable', savedSearches: [],
  setTheme: (theme) => set({ theme }),
  toggleDensity: () => set((state) => ({ density: state.density === 'compact' ? 'comfortable' : 'compact' })),
  saveSearch: (search) => set((state) => ({ savedSearches: [...state.savedSearches.filter((item) => item.id !== search.id), search] })),
  removeSearch: (id) => set((state) => ({ savedSearches: state.savedSearches.filter((item) => item.id !== id) }))
}), { name: 'kah-ui' }))

