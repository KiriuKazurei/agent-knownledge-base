import path from 'node:path'

export function installedDataRoot(value: string, installation: string): string {
  if (!path.isAbsolute(value)) throw new Error('Installed dataRoot must be an absolute path.')
  const configured = path.resolve(value)
  const contains = (parent: string, child: string) => {
    const relative = path.relative(parent, child)
    return !relative || (!relative.startsWith(`..${path.sep}`) && relative !== '..' && !path.isAbsolute(relative))
  }
  if (contains(installation, configured) || contains(configured, installation)) {
    throw new Error('Installed dataRoot must be separate from the application directory and its parents so updates and restore preserve knowledge.')
  }
  return configured
}
