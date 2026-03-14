import { useState, useCallback } from 'react';

interface UseFormStateReturn<T> {
  formData: T;
  setFormData: React.Dispatch<React.SetStateAction<T>>;
  updateField: <K extends keyof T>(key: K, value: T[K]) => void;
  resetForm: (data?: T) => void;
}

export function useFormState<T>(initialState: T): UseFormStateReturn<T> {
  const [formData, setFormData] = useState<T>(initialState);

  const updateField = useCallback(<K extends keyof T>(key: K, value: T[K]) => {
    setFormData(prev => ({ ...prev, [key]: value }));
  }, []);

  const resetForm = useCallback((data?: T) => {
    setFormData(data ?? initialState);
  }, [initialState]);

  return {
    formData,
    setFormData,
    updateField,
    resetForm,
  };
}
