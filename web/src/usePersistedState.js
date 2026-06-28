import { useEffect, useState } from 'react'

// usePersistedState behaves like useState but mirrors the value to localStorage,
// so selections survive tab switches and page reloads.
export default function usePersistedState(key, initial) {
  const [value, setValue] = useState(() => {
    try {
      const stored = localStorage.getItem(key)
      return stored !== null ? JSON.parse(stored) : initial
    } catch {
      return initial
    }
  })

  useEffect(() => {
    try {
      localStorage.setItem(key, JSON.stringify(value))
    } catch {
      // ignore write failures (private mode / quota)
    }
  }, [key, value])

  return [value, setValue]
}
